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

package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/anish/omegle/backend/golang/internal/automod"
	"github.com/anish/omegle/backend/golang/internal/observability"
	"github.com/anish/omegle/backend/golang/internal/storage"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("pairline-go-report-worker")

type IngestWorker struct {
	db            *storage.Database
	redisClient   redis.UniversalClient
	autoModerator *automod.Worker
	consumerName  string
}

func NewIngestWorker(db *storage.Database, redisClient redis.UniversalClient, autoModerator *automod.Worker) *IngestWorker {
	if db == nil || redisClient == nil {
		return nil
	}

	return &IngestWorker{
		db:            db,
		redisClient:   redisClient,
		autoModerator: autoModerator,
		consumerName:  resolveConsumerName(),
	}
}

func (w *IngestWorker) Start(ctx context.Context) {
	if w == nil {
		return
	}

	streamName := IngestStreamName()
	groupName := WorkerGroupName()

	go w.superviseReclaimer(ctx, streamName, groupName, w.consumerName)
	go w.superviseConsumer(ctx, streamName, groupName, w.consumerName)
}

func (w *IngestWorker) superviseConsumer(ctx context.Context, streamName, groupName, consumerName string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Printf("CRITICAL: report ingest consumer panicked: %v\n%s", recovered, debug.Stack())
				}
			}()

			w.consume(ctx, streamName, groupName, consumerName)
		}()

		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
			log.Println("Restarting report ingest consumer after recovery...")
		}
	}
}

func (w *IngestWorker) superviseReclaimer(ctx context.Context, streamName, groupName, consumerName string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Printf("CRITICAL: report ingest reclaimer panicked: %v\n%s", recovered, debug.Stack())
				}
			}()

			w.reclaimOrphaned(ctx, streamName, groupName, consumerName)
		}()

		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
			log.Println("Restarting report ingest reclaimer after recovery...")
		}
	}
}

func (w *IngestWorker) consume(ctx context.Context, streamName, groupName, consumerName string) {
	log.Printf("Report ingest consumer starting: group=%s consumer=%s", groupName, consumerName)

	err := w.redisClient.XGroupCreateMkStream(ctx, streamName, groupName, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		log.Printf("Failed to create report ingest consumer group: %v", err)
	}

	w.recoverPending(ctx, streamName, groupName, consumerName)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		res, err := w.redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    groupName,
			Consumer: consumerName,
			Streams:  []string{streamName, ">"},
			Count:    10,
			Block:    2 * time.Second,
		}).Result()
		if err != nil {
			if !errors.Is(err, redis.Nil) {
				log.Printf("Error reading from report ingest stream: %v", err)
				time.Sleep(1 * time.Second)
			}
			continue
		}

		for _, stream := range res {
			for _, msg := range stream.Messages {
				w.processMessage(ctx, streamName, groupName, msg)
			}
		}
	}
}

func (w *IngestWorker) recoverPending(ctx context.Context, streamName, groupName, consumerName string) {
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

		res, err := w.redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    groupName,
			Consumer: consumerName,
			Streams:  []string{streamName, "0"},
			Count:    10,
		}).Result()
		if err != nil {
			if !errors.Is(err, redis.Nil) {
				log.Printf("Error recovering pending report messages: %v", err)
			}
			return
		}

		if len(res) == 0 || len(res[0].Messages) == 0 {
			log.Println("Pending report message recovery complete")
			return
		}

		hadFailure := false
		for _, msg := range res[0].Messages {
			if !w.processMessage(ctx, streamName, groupName, msg) {
				hadFailure = true
			}
		}

		if hadFailure {
			if backoff == 0 {
				backoff = 1 * time.Second
			} else {
				backoff = min(backoff*2, maxBackoff)
			}
			log.Printf("Report ingest recovery backing off for %v after retryable failure", backoff)
			continue
		}

		backoff = 0
	}
}

func (w *IngestWorker) reclaimOrphaned(ctx context.Context, streamName, groupName, consumerName string) {
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
			msgs, newStart, err := w.redisClient.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream:   streamName,
				Group:    groupName,
				Consumer: consumerName,
				MinIdle:  5 * time.Minute,
				Start:    startID,
				Count:    10,
			}).Result()
			if err != nil {
				if !errors.Is(err, redis.Nil) {
					log.Printf("Error auto-claiming orphaned report messages: %v", err)
				}
				break
			}

			for _, msg := range msgs {
				log.Printf("Reclaimed orphaned report message %s", msg.ID)
				w.processMessage(ctx, streamName, groupName, msg)
			}

			if newStart == "0-0" || len(msgs) == 0 {
				break
			}
			startID = newStart
		}
	}
}

func (w *IngestWorker) processMessage(ctx context.Context, streamName, groupName string, msg redis.XMessage) bool {
	ctx, span := tracer.Start(ctx, "worker.processReport")
	defer span.End()

	span.SetAttributes(attribute.String("stream.message_id", msg.ID))

	payload, ok := msg.Values["payload"].(string)
	if !ok {
		log.Printf("Invalid payload in report stream message %s", msg.ID)
		span.SetStatus(codes.Error, "invalid payload")
		return w.ackAndDelete(ctx, streamName, groupName, msg.ID)
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
		return w.ackAndDelete(ctx, streamName, groupName, msg.ID)
	}

	ingestMessageID := msg.ID
	report := storage.Report{
		IngestMessageID:          &ingestMessageID,
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
		CreatedAt:                reportCreatedAt(data.Timestamp),
	}

	if err := w.db.GetDB().WithContext(ctx).Create(&report).Error; err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			existingReportID, lookupErr := w.findExistingReportIDByMessage(ctx, msg.ID)
			if lookupErr != nil {
				log.Printf("Duplicate report ingest message %s detected but existing report lookup failed: %v", msg.ID, lookupErr)
				span.RecordError(lookupErr)
				span.SetStatus(codes.Error, "duplicate lookup failed")
				return false
			}

			log.Printf("Duplicate report ingest message %s detected, reusing report %s", msg.ID, existingReportID)
			span.SetAttributes(
				attribute.Bool("report.duplicate", true),
				attribute.String("report.id", existingReportID),
			)
			w.enqueueAutoModeration(existingReportID)
			return w.ackAndDelete(ctx, streamName, groupName, msg.ID)
		}

		log.Printf("Failed to insert report into database: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "db insert failed")
		return false
	}

	span.SetAttributes(attribute.String("report.id", report.ID))

	observability.RecordBusinessEvent(
		ctx,
		"report.db_inserted",
	)

	w.enqueueAutoModeration(report.ID)

	return w.ackAndDelete(ctx, streamName, groupName, msg.ID)
}

func (w *IngestWorker) ackAndDelete(ctx context.Context, streamName, groupName, messageID string) bool {
	if err := w.redisClient.XAck(ctx, streamName, groupName, messageID).Err(); err != nil {
		log.Printf("Failed to ack report stream message %s: %v", messageID, err)
		return false
	}

	if err := w.redisClient.XDel(ctx, streamName, messageID).Err(); err != nil {
		log.Printf("Failed to delete report stream message %s after ack: %v", messageID, err)
	}

	return true
}

func (w *IngestWorker) findExistingReportIDByMessage(ctx context.Context, messageID string) (string, error) {
	if w == nil || w.db == nil || strings.TrimSpace(messageID) == "" {
		return "", errors.New("invalid duplicate report lookup state")
	}

	var report storage.Report
	if err := w.db.GetDB().
		WithContext(ctx).
		Select("id").
		Where("ingest_message_id = ?", messageID).
		Take(&report).Error; err != nil {
		return "", err
	}

	return report.ID, nil
}

func resolveConsumerName() string {
	hostname := strings.TrimSpace(os.Getenv("HOSTNAME"))
	if hostname == "" {
		hostname = fmt.Sprintf("anon-%d", rand.IntN(100000))
	}
	return "worker-" + hostname
}

func reportCreatedAt(timestampMillis int64) time.Time {
	if timestampMillis <= 0 {
		return time.Now().UTC()
	}
	return time.UnixMilli(timestampMillis).UTC()
}

func (w *IngestWorker) enqueueAutoModeration(reportID string) {
	if w == nil || strings.TrimSpace(reportID) == "" {
		return
	}

	w.autoModerator.Enqueue(reportID)
}
