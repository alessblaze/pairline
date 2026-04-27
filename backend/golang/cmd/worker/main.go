// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/anish/omegle/backend/golang/internal/automod"
	"github.com/anish/omegle/backend/golang/internal/config"
	"github.com/anish/omegle/backend/golang/internal/observability"
	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/storage"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("pairline-go-worker")

func main() {
	config.LoadDotEnvIfEnabled()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if shutdownTracing, err := observability.InitTracing(ctx, "pairline-go-worker"); err != nil {
		log.Printf("Warning: failed to initialize OpenTelemetry tracing: %v", err)
	} else {
		defer shutdownTracing(context.Background())
	}

	if shutdownMetrics, err := observability.InitMetrics(ctx, "pairline-go-worker"); err != nil {
		log.Printf("Warning: failed to initialize OpenTelemetry metrics: %v", err)
	} else {
		defer shutdownMetrics(context.Background())
	}

	db := storage.NewDatabase()
	redisClient := appredis.NewClient()

	autoModerator := automod.NewWorker(db.GetDB(), redisClient)

	autoModerator.Start(ctx)

	// Run the stream consumer in a supervised goroutine so a panic
	// doesn't silently kill report processing while the worker appears
	// healthy from the outside.
	go superviseConsumer(ctx, db, redisClient.GetClient(), autoModerator)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Omegle Go Database Worker started")
	<-sigChan
	log.Println("Shutting down worker...")
	cancel()

	// Allow some time for graceful shutdown
	time.Sleep(2 * time.Second)
	db.Close()
	redisClient.Close()
}

// superviseConsumer runs consumeReportsStream in a loop with panic
// recovery. If the consumer panics, it logs the stack trace and
// restarts after a brief delay instead of letting the goroutine die.
func superviseConsumer(ctx context.Context, db *storage.Database, redisClient redis.UniversalClient, autoModerator *automod.Worker) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("CRITICAL: consumeReportsStream panicked: %v\n%s", r, debug.Stack())
				}
			}()
			consumeReportsStream(ctx, db, redisClient, autoModerator)
		}()

		// If we get here, either the consumer returned (ctx cancelled)
		// or it panicked. Back off before restarting.
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
			log.Println("Restarting stream consumer after recovery...")
		}
	}
}

func consumeReportsStream(ctx context.Context, db *storage.Database, redisClient redis.UniversalClient, autoModerator *automod.Worker) {
	streamName := "stream:reports:ingest"
	groupName := "db_workers"
	consumerName := resolveConsumerName()

	log.Printf("Stream consumer starting: group=%s consumer=%s", groupName, consumerName)

	// Use "0" instead of "$" so the group starts from the beginning of
	// the stream. If the group already exists this is a no-op (BUSYGROUP).
	// Using "$" causes messages added between group creation and the
	// second worker joining to be invisible to the late-joining worker.
	err := redisClient.XGroupCreateMkStream(ctx, streamName, groupName, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		log.Printf("Failed to create consumer group: %v", err)
	}

	// Recover any pending messages from a previous crash before reading new ones.
	recoverPending(ctx, redisClient, db, autoModerator, streamName, groupName, consumerName)

	// Periodically reclaim stale pending entries from dead consumers.
	go reclaimOrphaned(ctx, redisClient, db, autoModerator, streamName, groupName, consumerName)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		res, err := redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    groupName,
			Consumer: consumerName,
			Streams:  []string{streamName, ">"},
			Count:    10,
			Block:    2 * time.Second,
		}).Result()

		if err != nil {
			if !errors.Is(err, redis.Nil) {
				log.Printf("Error reading from stream: %v", err)
				time.Sleep(1 * time.Second)
			}
			continue
		}

		for _, stream := range res {
			for _, msg := range stream.Messages {
				processMessage(ctx, redisClient, db, autoModerator, streamName, groupName, msg)
			}
		}
	}
}

// recoverPending reclaims messages that were delivered to this consumer
// but never acknowledged (e.g. due to a crash). It reads from ID "0"
// which returns all pending entries for this consumer, then processes
// and acks each one.
//
// Applies exponential backoff on persistent DB failures to avoid a
// tight busy-loop when Postgres is unavailable.
func recoverPending(ctx context.Context, redisClient redis.UniversalClient, db *storage.Database, autoModerator *automod.Worker, streamName, groupName, consumerName string) {
	const maxBackoff = 30 * time.Second
	backoff := time.Duration(0)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if backoff > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}

		res, err := redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    groupName,
			Consumer: consumerName,
			Streams:  []string{streamName, "0"},
			Count:    10,
		}).Result()

		if err != nil {
			if !errors.Is(err, redis.Nil) {
				log.Printf("Error recovering pending messages: %v", err)
			}
			return
		}

		if len(res) == 0 || len(res[0].Messages) == 0 {
			log.Println("Pending message recovery complete")
			return
		}

		hadFailure := false
		for _, msg := range res[0].Messages {
			if !processMessage(ctx, redisClient, db, autoModerator, streamName, groupName, msg) {
				hadFailure = true
			}
		}

		if hadFailure {
			// Exponential backoff: 1s → 2s → 4s → ... → 30s cap
			if backoff == 0 {
				backoff = 1 * time.Second
			} else {
				backoff = min(backoff*2, maxBackoff)
			}
			log.Printf("DB failure during recovery, backing off %v", backoff)
		} else {
			backoff = 0
		}
	}
}

// reclaimOrphaned periodically uses XAUTOCLAIM to steal stale pending
// entries from dead consumers (e.g. a worker that was replaced with a
// new container/hostname). Messages idle longer than 5 minutes are
// claimed by this consumer and processed.
func reclaimOrphaned(ctx context.Context, redisClient redis.UniversalClient, db *storage.Database, autoModerator *automod.Worker, streamName, groupName, consumerName string) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		startID := "0-0"
		for {
			msgs, newStart, err := redisClient.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream:   streamName,
				Group:    groupName,
				Consumer: consumerName,
				MinIdle:  5 * time.Minute,
				Start:    startID,
				Count:    10,
			}).Result()

			if err != nil {
				if !errors.Is(err, redis.Nil) {
					log.Printf("Error auto-claiming orphaned messages: %v", err)
				}
				break
			}

			for _, msg := range msgs {
				log.Printf("Reclaimed orphaned message %s", msg.ID)
				processMessage(ctx, redisClient, db, autoModerator, streamName, groupName, msg)
			}

			if newStart == "0-0" || len(msgs) == 0 {
				break
			}
			startID = newStart
		}
	}
}

// processMessage handles a single stream message. Returns true on
// success (message acked), false if a retryable error occurred (message
// left in the PEL for later retry).
func processMessage(ctx context.Context, redisClient redis.UniversalClient, db *storage.Database, autoModerator *automod.Worker, streamName, groupName string, msg redis.XMessage) bool {
	ctx, span := tracer.Start(ctx, "worker.processReport")
	defer span.End()

	span.SetAttributes(attribute.String("stream.message_id", msg.ID))

	payload, ok := msg.Values["payload"].(string)
	if !ok {
		log.Printf("Invalid payload in stream message %s", msg.ID)
		span.SetStatus(codes.Error, "invalid payload")
		redisClient.XAck(ctx, streamName, groupName, msg.ID)
		redisClient.XDel(ctx, streamName, msg.ID)
		return true
	}

	var data struct {
		ReporterSessionID string `json:"reporter_session_id"`
		ReportedSessionID string `json:"reported_session_id"`
		ReporterIP        string `json:"reporter_ip"`
		ReportedIP        string `json:"reported_ip"`
		Reason            string `json:"reason"`
		Description       string `json:"description"`
		ChatLog           string `json:"chat_log"`
		Timestamp         int64  `json:"timestamp"`
	}

	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		log.Printf("Failed to unmarshal report payload: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "unmarshal failed")
		redisClient.XAck(ctx, streamName, groupName, msg.ID)
		redisClient.XDel(ctx, streamName, msg.ID)
		return true
	}

	report := storage.Report{
		ReporterSessionID:        data.ReporterSessionID,
		ReportedSessionID:        data.ReportedSessionID,
		ReporterIP:               data.ReporterIP,
		ReportedIP:               data.ReportedIP,
		Reason:                   data.Reason,
		Description:              data.Description,
		ChatLog:                  data.ChatLog,
		Status:                   "pending",
		AutoModerationState:      "pending",
		AutoModerationDecision:   "",
		AutoModerationCategories: "[]",
		CreatedAt:                time.UnixMilli(data.Timestamp),
	}

	if err := db.GetDB().WithContext(ctx).Create(&report).Error; err != nil {
		// Guard against duplicate insert on PEL retry: if Postgres rejects
		// with a unique-constraint violation, treat the insert as idempotent
		// and ack the message instead of retrying forever.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			log.Printf("Duplicate report detected for message %s, acking", msg.ID)
			span.SetAttributes(attribute.Bool("report.duplicate", true))
			redisClient.XAck(ctx, streamName, groupName, msg.ID)
			redisClient.XDel(ctx, streamName, msg.ID)
			return true
		}

		log.Printf("Failed to insert report into database: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "db insert failed")
		// Retryable failure — leave in PEL.
		return false
	}

	span.SetAttributes(attribute.String("report.id", report.ID))

	observability.RecordBusinessEvent(
		ctx,
		"report.db_inserted",
	)

	// Kick off the auto-moderation pipeline
	autoModerator.Enqueue(report.ID)

	// Acknowledge and remove the message from the stream
	redisClient.XAck(ctx, streamName, groupName, msg.ID)
	redisClient.XDel(ctx, streamName, msg.ID)
	return true
}

// resolveConsumerName builds a unique consumer name for this worker
// instance. Falls back to a random suffix if HOSTNAME is unset or empty.
func resolveConsumerName() string {
	hostname := strings.TrimSpace(os.Getenv("HOSTNAME"))
	if hostname == "" {
		hostname = fmt.Sprintf("anon-%d", rand.IntN(100000))
	}
	return "worker-" + hostname
}
