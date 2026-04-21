//go:build stress

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/google/uuid"
)

type signalingStressSession struct {
	id        string
	route     appredis.SessionRoute
	delivered atomic.Int64
	client    *SignalingClient
}

type signalingStressJob struct {
	targetID string
	payload  []byte
}

const localStressBatchSize = 16

func TestRedisSignalingLiveStress(t *testing.T) {
	if os.Getenv("RUN_LIVE_REDIS_STRESS") == "" {
		t.Skip("set RUN_LIVE_REDIS_STRESS=1 to run the live Redis signaling stress test")
	}
	if strings.TrimSpace(os.Getenv("REDIS_CLUSTER_NODES")) == "" {
		t.Skip("REDIS_CLUSTER_NODES is required for the live Redis signaling stress test")
	}

	sessionCount := stressEnvInt("STRESS_SESSION_COUNT", 120)
	localSessionCount := stressEnvInt("STRESS_LOCAL_SESSION_COUNT", sessionCount/2)
	localSessionCount = max(1, min(localSessionCount, sessionCount))
	remoteSessionCount := sessionCount - localSessionCount
	localSendsPerSession := stressEnvInt("STRESS_LOCAL_SENDS_PER_SESSION", 80)
	remoteSendsPerSession := stressEnvInt("STRESS_REMOTE_SENDS_PER_SESSION", maxPendingMsgs*2)
	concurrency := stressEnvInt("STRESS_CONCURRENCY", 32)
	concurrency = max(1, concurrency)

	senderHub := NewRedisSignalingHub()
	redisClient := appredis.NewClient()
	senderHub.redis = redisClient

	receiverHub := NewRedisSignalingHub()
	receiverHub.Start(redisClient)

	t.Cleanup(func() {
		receiverHub.Stop()
		senderHub.Stop()
		if err := redisClient.Close(); err != nil {
			t.Fatalf("redis close returned error: %v", err)
		}
	})

	runID := fmt.Sprintf("stress:%d:%s", time.Now().UnixMilli(), uuid.NewString())
	t.Logf(
		"starting signaling stress run run_id=%s sessions=%d locals=%d remotes=%d concurrency=%d",
		runID,
		sessionCount,
		localSessionCount,
		remoteSessionCount,
		concurrency,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	totalStartedAt := time.Now()

	localSessions := make([]*signalingStressSession, 0, localSessionCount)
	remoteSessions := make([]*signalingStressSession, 0, remoteSessionCount)
	allSessions := make([]*signalingStressSession, 0, sessionCount)

	seedStartedAt := time.Now()
	for i := 0; i < sessionCount; i++ {
		session := &signalingStressSession{
			id: uuid.NewString(),
			route: appredis.SessionRoute{
				Mode:  "text",
				Shard: i % 8,
			},
		}
		allSessions = append(allSessions, session)
		if i < localSessionCount {
			localSessions = append(localSessions, session)
		} else {
			remoteSessions = append(remoteSessions, session)
		}

		locator := fmt.Sprintf("%s|%d", session.route.Mode, session.route.Shard)
		if err := redisClient.GetClient().Set(ctx, appredis.SessionLocatorKey(session.id), locator, 5*time.Minute).Err(); err != nil {
			t.Fatalf("set locator for %s returned error: %v", session.id, err)
		}
	}
	seedDuration := time.Since(seedStartedAt)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		for _, session := range allSessions {
			receiverHub.Unregister(session.id)
			senderHub.Unregister(session.id)
			_ = redisClient.GetClient().Del(
				cleanupCtx,
				appredis.SessionLocatorKey(session.id),
				appredis.WebRTCOwnerKey(session.id, session.route),
				appredis.WebRTCReadyKey(session.id, session.route),
				appredis.WebRTCPendingKey(session.id, session.route),
			).Err()
		}
	})

	registerLocalStartedAt := time.Now()
	for _, session := range localSessions {
		client, pending, err := receiverHub.Register(session.id, nil)
		if err != nil {
			t.Fatalf("register local session %s returned error: %v", session.id, err)
		}
		if len(pending) != 0 {
			t.Fatalf("register local session %s returned pending=%d, want 0", session.id, len(pending))
		}

		session.client = client
		go drainStressClient(client, &session.delivered)
	}
	registerLocalDuration := time.Since(registerLocalStartedAt)

	warmupStartedAt := time.Now()
	time.Sleep(250 * time.Millisecond)
	warmupDuration := time.Since(warmupStartedAt)

	localSendStartedAt := time.Now()
	var localDeliveryDuration time.Duration
	deliveredPerSession := 0

	for deliveredPerSession < localSendsPerSession {
		batchSize := min(localStressBatchSize, localSendsPerSession-deliveredPerSession)
		batchStart := deliveredPerSession

		err := runStressJobs(concurrency, func(jobs chan<- signalingStressJob) {
			for sendOffset := 0; sendOffset < batchSize; sendOffset++ {
				sendIndex := batchStart + sendOffset
				for _, session := range localSessions {
					jobs <- signalingStressJob{
						targetID: session.id,
						payload:  mustMarshalStressPayload(runID, session.id, sendIndex),
					}
				}
			}
		}, func(job signalingStressJob) error {
			return senderHub.SendOrQueue(job.targetID, job.payload)
		})
		if err != nil {
			t.Fatalf("local stress send returned error: %v", err)
		}

		deliveredPerSession += batchSize
		deliveryStartedAt := time.Now()
		waitForStressLocalDelivery(t, localSessions, int64(deliveredPerSession), 20*time.Second)
		localDeliveryDuration += time.Since(deliveryStartedAt)
	}

	localSendDuration := time.Since(localSendStartedAt)

	remoteSendStartedAt := time.Now()
	if err := runStressJobs(concurrency, func(jobs chan<- signalingStressJob) {
		for sendIndex := 0; sendIndex < remoteSendsPerSession; sendIndex++ {
			for _, session := range remoteSessions {
				jobs <- signalingStressJob{
					targetID: session.id,
					payload:  mustMarshalStressPayload(runID, session.id, sendIndex),
				}
			}
		}
	}, func(job signalingStressJob) error {
		return senderHub.SendOrQueue(job.targetID, job.payload)
	}); err != nil {
		t.Fatalf("remote stress send returned error: %v", err)
	}
	remoteSendDuration := time.Since(remoteSendStartedAt)

	pendingCheckStartedAt := time.Now()
	for _, session := range remoteSessions {
		client, pending, err := senderHub.Register(session.id, nil)
		if err != nil {
			t.Fatalf("register remote session %s returned error: %v", session.id, err)
		}

		wantPending := min(remoteSendsPerSession, maxPendingMsgs)
		if len(pending) != wantPending {
			t.Fatalf("remote session %s pending=%d, want %d", session.id, len(pending), wantPending)
		}

		if client != nil {
			client.close()
		}
		senderHub.Unregister(session.id)
	}
	pendingCheckDuration := time.Since(pendingCheckStartedAt)

	elapsed := time.Since(totalStartedAt)
	t.Logf(
		"signaling stress run completed run_id=%s duration=%s local_messages=%d remote_messages=%d",
		runID,
		elapsed,
		len(localSessions)*localSendsPerSession,
		len(remoteSessions)*remoteSendsPerSession,
	)
	t.Logf("  seed_ms=%d", seedDuration.Milliseconds())
	t.Logf("  register_local_ms=%d", registerLocalDuration.Milliseconds())
	t.Logf("  warmup_ms=%d", warmupDuration.Milliseconds())
	t.Logf("  local_send_ms=%d", localSendDuration.Milliseconds())
	t.Logf("  local_delivery_ms=%d", localDeliveryDuration.Milliseconds())
	t.Logf("  remote_send_ms=%d", remoteSendDuration.Milliseconds())
	t.Logf("  pending_check_ms=%d", pendingCheckDuration.Milliseconds())
	t.Logf("  total_ms=%d", elapsed.Milliseconds())
}

func drainStressClient(client *SignalingClient, delivered *atomic.Int64) {
	if client == nil || delivered == nil {
		return
	}

	for {
		select {
		case <-client.done:
			return
		case _, ok := <-client.send:
			if !ok {
				return
			}
			delivered.Add(1)
		}
	}
}

func waitForStressLocalDelivery(
	t *testing.T,
	sessions []*signalingStressSession,
	wantPerSession int64,
	timeout time.Duration,
) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		allDelivered := true
		for _, session := range sessions {
			if got := session.delivered.Load(); got != wantPerSession {
				allDelivered = false
				break
			}
		}

		if allDelivered {
			return
		}

		if time.Now().After(deadline) {
			for _, session := range sessions {
				t.Logf("session=%s delivered=%d want=%d", session.id, session.delivered.Load(), wantPerSession)
			}
			t.Fatalf("timed out waiting for local stress delivery after %s", timeout)
		}

		time.Sleep(25 * time.Millisecond)
	}
}

func mustMarshalStressPayload(runID, sessionID string, index int) []byte {
	payload, err := json.Marshal(map[string]any{
		"type":       "offer",
		"run_id":     runID,
		"target":     sessionID,
		"message_id": strconv.Itoa(index),
	})
	if err != nil {
		panic(err)
	}
	return payload
}

func runStressJobs(
	concurrency int,
	enqueue func(chan<- signalingStressJob),
	send func(signalingStressJob) error,
) error {
	jobs := make(chan signalingStressJob, concurrency*2)
	var workerWG sync.WaitGroup
	var sendErr atomic.Value

	for workerIndex := 0; workerIndex < concurrency; workerIndex++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for job := range jobs {
				if err := send(job); err != nil {
					sendErr.CompareAndSwap(nil, err)
				}
			}
		}()
	}

	enqueue(jobs)
	close(jobs)
	workerWG.Wait()

	if err, _ := sendErr.Load().(error); err != nil {
		return err
	}

	return nil
}

func stressEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}

	return value
}
