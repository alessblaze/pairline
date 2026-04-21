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

package handlers

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anish/omegle/backend/golang/internal/observability"
	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var allowedSignalTypes = map[string]struct{}{
	"ready":  {},
	"offer":  {},
	"answer": {},
	"ice":    {},
}

var errSessionAlreadyOwned = errors.New("session signaling already connected elsewhere")
var errOwnershipLost = errors.New("session signaling ownership lost")
var errRedisUnavailable = errors.New("redis unavailable for remote signaling")

const (
	writeWait         = 10 * time.Second
	pongWait          = 60 * time.Second
	pingPeriod        = 25 * time.Second
	maxMessageSize    = 64 * 1024
	maxPendingMsgs    = 64
	ownerTTL          = 75 * time.Second
	pendingSignalTTL  = 30 * time.Second
	readySignalTTL    = 60 * time.Second
	ownerRefreshEvery = 20 * time.Second
	signalRateLimit   = 20.0
	signalBurstLimit  = 40.0
	clientShardCount  = 256
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false
		}

		allowedOriginsStr := os.Getenv("CORS_ORIGIN")
		if allowedOriginsStr == "" {
			log.Printf("WebSocket rejected origin %s because CORS_ORIGIN is not configured", origin)
			return false
		}

		for _, allowed := range strings.Split(allowedOriginsStr, ",") {
			trimmed := strings.TrimSpace(allowed)
			if trimmed == origin || trimmed+"/" == origin || trimmed == origin+"/" {
				return true
			}
		}

		log.Printf("WebSocket rejected origin: %s", origin)
		return false
	},
}

type SignalingClient struct {
	Conn      *websocket.Conn
	send      chan outboundMessage
	closeOnce sync.Once
	done      chan struct{}
}

type outboundMessage struct {
	messageType int
	data        []byte
}

type signalRateLimiter struct {
	tokens     float64
	lastRefill time.Time
}

type sessionEntry struct {
	client   *SignalingClient
	claiming bool
}

type clientShard struct {
	mu    sync.RWMutex
	items map[string]*sessionEntry
}

func newSignalRateLimiter(now time.Time) *signalRateLimiter {
	return &signalRateLimiter{
		tokens:     signalBurstLimit,
		lastRefill: now,
	}
}

func (l *signalRateLimiter) allow(now time.Time) bool {
	elapsedSeconds := now.Sub(l.lastRefill).Seconds()
	l.lastRefill = now
	l.tokens += elapsedSeconds * signalRateLimit
	if l.tokens > signalBurstLimit {
		l.tokens = signalBurstLimit
	}

	if l.tokens < 1 {
		return false
	}

	l.tokens--
	return true
}

func newSignalingClient(conn *websocket.Conn) *SignalingClient {
	return &SignalingClient{
		Conn: conn,
		send: make(chan outboundMessage, maxPendingMsgs),
		done: make(chan struct{}),
	}
}

func (c *SignalingClient) enqueue(messageType int, data []byte) bool {
	message := outboundMessage{
		messageType: messageType,
		data:        append([]byte(nil), data...),
	}

	select {
	case <-c.done:
		return false
	default:
	}

	select {
	case c.send <- message:
		return true
	case <-c.done:
		return false
	default:
		return false
	}
}

func (c *SignalingClient) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.Conn != nil {
			_ = c.Conn.Close()
		}
	})
}

func (c *SignalingClient) writeMessage(messageType int, data []byte) error {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return c.Conn.WriteMessage(messageType, data)
}

func writeCloseControl(conn *websocket.Conn, code int, text string) error {
	if conn == nil {
		return nil
	}

	deadline := time.Now().Add(writeWait)
	message := websocket.FormatCloseMessage(code, text)
	return conn.WriteControl(websocket.CloseMessage, message, deadline)
}

func (c *SignalingClient) writePump(onTick func(time.Time), onError func()) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case outbound, ok := <-c.send:
			if !ok {
				return
			}
			if err := c.writeMessage(outbound.messageType, outbound.data); err != nil {
				log.Printf("WebRTC WS write failed: %v", err)
				if onError != nil {
					onError()
				}
				c.close()
				return
			}
		case <-ticker.C:
			if onTick != nil {
				onTick(time.Now())
			}
			if err := c.writeMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("WebRTC WS ping failed: %v", err)
				if onError != nil {
					onError()
				}
				c.close()
				return
			}
		}
	}
}

type redisSignalEnvelope struct {
	TargetSessionID string          `json:"target_session_id"`
	Payload         json.RawMessage `json:"payload"`
}

type RedisSignalingHub struct {
	shards     [clientShardCount]clientShard
	redis      *appredis.Client
	instanceID string
	startOnce  sync.Once
	stopOnce   sync.Once
	stopCh     chan struct{}
	subscriber sync.WaitGroup
}

var Signaling = NewRedisSignalingHub()

func NewRedisSignalingHub() *RedisSignalingHub {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "go-worker"
	}

	instanceID := hostname + ":" + time.Now().UTC().Format("20060102150405.000000000")

	h := &RedisSignalingHub{
		instanceID: instanceID,
		stopCh:     make(chan struct{}),
	}

	for i := range h.shards {
		h.shards[i].items = make(map[string]*sessionEntry)
	}

	return h
}

func shardIndex(sessionID string) uint32 {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(sessionID))
	return hasher.Sum32() % clientShardCount
}

func (h *RedisSignalingHub) clientShard(sessionID string) *clientShard {
	return &h.shards[shardIndex(sessionID)]
}

func (h *RedisSignalingHub) Start(redisClient *appredis.Client) {
	h.startOnce.Do(func() {
		h.redis = redisClient
		h.subscriber.Add(1)
		go func() {
			defer h.subscriber.Done()
			h.runSubscriber()
		}()
	})
}

func (h *RedisSignalingHub) Stop() {
	h.stopOnce.Do(func() {
		close(h.stopCh)
	})
	h.subscriber.Wait()
}

func (h *RedisSignalingHub) Register(sessionID string, conn *websocket.Conn) (*SignalingClient, [][]byte, error) {
	client := newSignalingClient(conn)
	cs := h.clientShard(sessionID)

	cs.mu.Lock()
	entry, exists := cs.items[sessionID]
	if !exists {
		entry = &sessionEntry{}
		cs.items[sessionID] = entry
	}
	if entry.client != nil || entry.claiming {
		cs.mu.Unlock()
		return nil, nil, errSessionAlreadyOwned
	}
	entry.claiming = true
	cs.mu.Unlock()

	pending, err := h.claimSession(sessionID)
	if err != nil {
		cs.mu.Lock()
		entry := cs.items[sessionID]
		if entry != nil {
			entry.claiming = false
			if entry.client == nil {
				delete(cs.items, sessionID)
			}
		}
		cs.mu.Unlock()
		return nil, nil, err
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	entry = cs.items[sessionID]
	if entry == nil {
		return nil, nil, errSessionAlreadyOwned
	}

	entry.claiming = false
	if entry.client != nil {
		return nil, nil, errSessionAlreadyOwned
	}

	entry.client = client
	return client, pending, nil
}

func (h *RedisSignalingHub) Unregister(sessionID string) {
	var client *SignalingClient
	cs := h.clientShard(sessionID)

	cs.mu.Lock()
	entry := cs.items[sessionID]
	if entry != nil {
		client = entry.client
		entry.client = nil
		if !entry.claiming {
			delete(cs.items, sessionID)
		}
	}
	cs.mu.Unlock()

	if client != nil {
		client.close()
	}

	if h.redis == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if route, err := appredis.ResolveSessionRoute(ctx, h.redis.GetClient(), sessionID); err == nil {
		_ = h.compareAndDelete(ctx, ownerKey(sessionID, route), h.instanceID)
		_ = h.redis.GetClient().Del(ctx, readyKey(sessionID, route)).Err()
	}
}

func (h *RedisSignalingHub) MarkReady(sessionID string) {
	if h.redis == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	route, err := appredis.ResolveSessionRoute(ctx, h.redis.GetClient(), sessionID)
	if err != nil {
		return
	}

	_ = h.redis.GetClient().Set(ctx, readyKey(sessionID, route), "1", readySignalTTL).Err()
}

func (h *RedisSignalingHub) IsReady(sessionID string) bool {
	if h.redis == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	route, err := appredis.ResolveSessionRoute(ctx, h.redis.GetClient(), sessionID)
	if err != nil {
		return false
	}

	exists, err := h.redis.GetClient().Exists(ctx, readyKey(sessionID, route)).Result()
	return err == nil && exists > 0
}

func (h *RedisSignalingHub) SendOrQueue(targetSessionID string, payload []byte) error {
	if h.dispatchLocal(targetSessionID, payload) {
		return nil
	}

	if h.redis == nil {
		return errRedisUnavailable
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	route, err := appredis.ResolveSessionRoute(ctx, h.redis.GetClient(), targetSessionID)
	if err != nil {
		return h.enqueuePendingIfStillRemote(ctx, targetSessionID, payload)
	}

	envelope, marshalErr := json.Marshal(redisSignalEnvelope{
		TargetSessionID: targetSessionID,
		Payload:         payload,
	})
	if marshalErr != nil {
		return marshalErr
	}

	const sendOrQueueScript = `
local owner = redis.call("GET", KEYS[1])
if owner and owner ~= "" and owner ~= ARGV[1] then
  local delivered = redis.call("PUBLISH", ARGV[2] .. owner, ARGV[3])
  if delivered and tonumber(delivered) and tonumber(delivered) > 0 then
    return 1
  end
end
redis.call("RPUSH", KEYS[2], ARGV[4])
redis.call("LTRIM", KEYS[2], -tonumber(ARGV[5]), -1)
redis.call("EXPIRE", KEYS[2], tonumber(ARGV[6]))
return 0
`

	_, evalErr := h.redis.GetClient().Eval(
		ctx,
		sendOrQueueScript,
		[]string{ownerKey(targetSessionID, route), pendingKey(targetSessionID, route)},
		h.instanceID,
		"webrtc:signal:",
		string(envelope),
		string(payload),
		maxPendingMsgs,
		int(pendingSignalTTL/time.Second),
	).Result()
	return evalErr
}

func (h *RedisSignalingHub) runSubscriber() {
	for {
		if h.stopped() {
			return
		}

		if h.redis == nil {
			if h.waitOrStop(time.Second) {
				return
			}
			continue
		}

		ctx := context.Background()
		pubsub := h.redis.GetClient().Subscribe(ctx, signalChannel(h.instanceID))
		_, err := pubsub.Receive(ctx)
		if err != nil {
			_ = pubsub.Close()

			if h.stopped() || errors.Is(err, redis.ErrClosed) {
				return
			}

			log.Printf("Redis signaling subscribe failed for %s: %v", h.instanceID, err)
			if h.waitOrStop(time.Second) {
				return
			}
			continue
		}

		ch := pubsub.Channel()
		log.Printf("Redis signaling subscriber active for %s", h.instanceID)
		subscriptionDone := make(chan struct{})

		go func() {
			select {
			case <-h.stopCh:
				_ = pubsub.Close()
			case <-subscriptionDone:
			}
		}()

		for msg := range ch {
			var envelope redisSignalEnvelope
			if err := json.Unmarshal([]byte(msg.Payload), &envelope); err != nil {
				log.Printf("Failed to decode Redis signaling envelope: %v", err)
				continue
			}

			if len(envelope.Payload) == 0 || envelope.TargetSessionID == "" {
				continue
			}

			if !h.dispatchLocal(envelope.TargetSessionID, envelope.Payload) {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				route, err := appredis.ResolveSessionRoute(ctx, h.redis.GetClient(), envelope.TargetSessionID)
				if err == nil {
					owner, ownerErr := h.redis.GetClient().Get(ctx, ownerKey(envelope.TargetSessionID, route)).Result()
					if ownerErr == nil && owner == h.instanceID {
						_ = h.enqueuePendingIfStillRemote(ctx, envelope.TargetSessionID, envelope.Payload)
					}
				}
				cancel()
			}
		}

		close(subscriptionDone)
		_ = pubsub.Close()

		if h.stopped() {
			return
		}

		if h.waitOrStop(time.Second) {
			return
		}
	}
}

func (h *RedisSignalingHub) stopped() bool {
	select {
	case <-h.stopCh:
		return true
	default:
		return false
	}
}

func (h *RedisSignalingHub) waitOrStop(delay time.Duration) bool {
	select {
	case <-h.stopCh:
		return true
	case <-time.After(delay):
		return false
	}
}

func (h *RedisSignalingHub) dispatchLocal(targetSessionID string, payload []byte) bool {
	cs := h.clientShard(targetSessionID)

	cs.mu.RLock()
	entry := cs.items[targetSessionID]
	var client *SignalingClient
	if entry != nil {
		client = entry.client
	}
	cs.mu.RUnlock()

	if client == nil {
		return false
	}

	if !client.enqueue(websocket.TextMessage, payload) {
		log.Printf("Dropping backed-up WS signal to %s and closing slow client", targetSessionID)
		h.Unregister(targetSessionID)
		return false
	}

	return true
}

func (h *RedisSignalingHub) isLocalSessionActive(sessionID string) bool {
	cs := h.clientShard(sessionID)
	cs.mu.RLock()
	entry := cs.items[sessionID]
	active := entry != nil && entry.client != nil
	cs.mu.RUnlock()
	return active
}

func (h *RedisSignalingHub) claimSession(sessionID string) ([][]byte, error) {
	if h.redis == nil {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	route, err := appredis.ResolveSessionRoute(ctx, h.redis.GetClient(), sessionID)
	if err != nil {
		return nil, err
	}

	const claimScript = `
local owner = redis.call("GET", KEYS[1])
if owner and owner ~= ARGV[1] then
  return {"owned", owner}
end
redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
redis.call("DEL", KEYS[2])
local pending = redis.call("LRANGE", KEYS[3], 0, tonumber(ARGV[3]) - 1)
redis.call("DEL", KEYS[3])
table.insert(pending, 1, "ok")
return pending
`

	result, err := h.redis.GetClient().Eval(
		ctx,
		claimScript,
		[]string{ownerKey(sessionID, route), readyKey(sessionID, route), pendingKey(sessionID, route)},
		h.instanceID,
		int(ownerTTL/time.Second),
		maxPendingMsgs,
	).Result()
	if err != nil {
		return nil, err
	}

	values, ok := result.([]interface{})
	if !ok || len(values) == 0 {
		return nil, errors.New("unexpected Redis claim response")
	}

	status, ok := values[0].(string)
	if !ok {
		return nil, errors.New("unexpected Redis claim status")
	}

	if status == "owned" {
		return nil, errSessionAlreadyOwned
	}

	if status != "ok" {
		return nil, errors.New("unexpected Redis claim status")
	}

	rawPending := values[1:]
	pending := make([][]byte, 0, len(rawPending))
	for _, item := range rawPending {
		value, ok := item.(string)
		if !ok {
			return nil, errors.New("unexpected Redis pending payload")
		}
		pending = append(pending, []byte(value))
	}

	return pending, nil
}

func (h *RedisSignalingHub) enqueuePendingIfStillRemote(ctx context.Context, sessionID string, payload []byte) error {
	if h.isLocalSessionActive(sessionID) {
		if h.dispatchLocal(sessionID, payload) {
			return nil
		}
	}
	return h.enqueuePending(ctx, sessionID, payload)
}

func (h *RedisSignalingHub) enqueuePending(ctx context.Context, sessionID string, payload []byte) error {
	if h.redis == nil {
		return errRedisUnavailable
	}

	route, err := appredis.ResolveSessionRoute(ctx, h.redis.GetClient(), sessionID)
	if err != nil {
		return err
	}

	pipe := h.redis.GetClient().TxPipeline()
	key := pendingKey(sessionID, route)
	pipe.RPush(ctx, key, string(payload))
	pipe.LTrim(ctx, key, -maxPendingMsgs, -1)
	pipe.Expire(ctx, key, pendingSignalTTL)
	_, err = pipe.Exec(ctx)
	return err
}

func (h *RedisSignalingHub) compareAndDelete(ctx context.Context, key string, expected string) error {
	const compareDeleteScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`
	return h.redis.GetClient().Eval(ctx, compareDeleteScript, []string{key}, expected).Err()
}

func (h *RedisSignalingHub) refreshOwnerKey(ctx context.Context, key string) error {
	if h.redis == nil {
		return errRedisUnavailable
	}

	const refreshOwnerScript = `
local owner = redis.call("GET", KEYS[1])
if owner == ARGV[1] then
  return redis.call("EXPIRE", KEYS[1], ARGV[2])
end
return 0
`

	result, err := h.redis.GetClient().Eval(
		ctx,
		refreshOwnerScript,
		[]string{key},
		h.instanceID,
		int(ownerTTL/time.Second),
	).Result()
	if err != nil {
		return err
	}

	switch value := result.(type) {
	case int64:
		if value == 0 {
			return errOwnershipLost
		}
	case int:
		if value == 0 {
			return errOwnershipLost
		}
	default:
		return errOwnershipLost
	}

	return nil
}

func ownerKey(sessionID string, route appredis.SessionRoute) string {
	return appredis.WebRTCOwnerKey(sessionID, route)
}

func readyKey(sessionID string, route appredis.SessionRoute) string {
	return appredis.WebRTCReadyKey(sessionID, route)
}

func pendingKey(sessionID string, route appredis.SessionRoute) string {
	return appredis.WebRTCPendingKey(sessionID, route)
}

func signalChannel(instanceID string) string {
	return "webrtc:signal:" + instanceID
}

type SignalingMessage struct {
	Type        string                 `json:"type"`
	ToSessionID string                 `json:"to_session_id"`
	Data        map[string]interface{} `json:"data"`
}

func WebRTCWebSocketHandlerGin(redisClient *appredis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		span := startHandlerSpan(c, "webrtc.signal.connect")
		handshakeSpanEnded := false
		defer func() {
			if !handshakeSpanEnded {
				span.End()
			}
		}()

		sessionID := c.Query("session_id")
		sessionToken := c.Query("session_token")
		span.SetAttributes(hashedAttribute("webrtc.session.ref", sessionID))
		if sessionID == "" || sessionToken == "" || len(sessionID) > 100 || len(sessionToken) > 128 || !uuidRe.MatchString(sessionID) {
			span.SetStatus(codes.Error, "invalid session format")
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id or session_token format"})
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if !verifySessionTokenWebRTC(ctx, redisClient.GetClient(), sessionID, sessionToken) {
			cancel()
			span.SetStatus(codes.Error, "invalid session")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
			return
		}
		cancel()

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "websocket upgrade failed")
			log.Printf("WebRTC WS Upgrade failed: %v", err)
			return
		}
		conn.SetReadLimit(maxMessageSize)
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(pongWait))
		})

		client, pendingMessages, err := Signaling.Register(sessionID, conn)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "signaling registration failed")
			log.Printf("Failed to register signaling session %s: %v", sessionID, err)
			_ = writeCloseControl(conn, websocket.ClosePolicyViolation, "already connected")
			_ = conn.Close()
			return
		}
		span.SetAttributes(attribute.String("webrtc.connection_state", "registered"))
		observability.AddWebRTCConnection(c.Request.Context(), 1)

		unregisterOnce := sync.Once{}
		unregister := func() {
			unregisterOnce.Do(func() {
				observability.AddWebRTCConnection(c.Request.Context(), -1)
				Signaling.Unregister(sessionID)
			})
		}

		defer func() {
			unregister()
			_ = conn.Close()
			log.Printf("WebRTC WS closed for session: %s", sessionID)
		}()

		log.Printf("WebRTC WS registered for session: %s", sessionID)

		sessionRouteCtx, sessionRouteCancel := context.WithTimeout(context.Background(), 2*time.Second)
		sessionRoute, routeErr := appredis.ResolveSessionRoute(sessionRouteCtx, redisClient.GetClient(), sessionID)
		sessionRouteCancel()
		if routeErr != nil {
			span.RecordError(routeErr)
			span.SetStatus(codes.Error, "session route resolution failed")
			log.Printf("WebRTC WS missing session route for %s: %v", sessionID, routeErr)
			return
		}
		span.SetAttributes(attribute.String("webrtc.connection_state", "ready"))
		span.End()
		handshakeSpanEnded = true
		c.Set("http.server.span.end_time", time.Now())
		connectedAt := time.Now()
		disconnectReason := "client_closed"

		ownershipJitter := time.Duration(time.Now().UnixNano() % int64(5*time.Second)) // 0-5s
		go client.writePump(func() func(time.Time) {
			nextOwnershipRefresh := time.Now().Add(ownershipJitter)
			return func(now time.Time) {
				if now.Before(nextOwnershipRefresh) {
					return
				}
				nextOwnershipRefresh = now.Add(ownerRefreshEvery)

				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				err := Signaling.refreshOwnerKey(ctx, ownerKey(sessionID, sessionRoute))
				cancel()
				if err != nil {
					log.Printf("WebRTC WS ownership refresh failed for %s: %v", sessionID, err)
					if errors.Is(err, errOwnershipLost) {
						disconnectReason = "ownership_lost"
						client.close()
					}
				}
			}
		}(), unregister)

		for _, pendingMessage := range pendingMessages {
			if !client.enqueue(websocket.TextMessage, pendingMessage) {
				log.Printf("Failed queueing pending WS signal to %s", sessionID)
				break
			}
		}

		limiter := newSignalRateLimiter(time.Now())

		for {
			_, messageData, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					disconnectReason = "unexpected_close"
					log.Printf("WebRTC WS connection closed unexpectedly for session: %s", sessionID)
				} else if closeErr, ok := err.(*websocket.CloseError); ok {
					disconnectReason = websocketCloseReason(closeErr.Code)
				} else {
					disconnectReason = "read_error"
				}
				break
			}

			if !limiter.allow(time.Now()) {
				disconnectReason = "rate_limited"
				log.Printf("WebRTC WS dropping spammy signaling client for session: %s", sessionID)
				break
			}

			var msg SignalingMessage
			if err := json.Unmarshal(messageData, &msg); err != nil {
				log.Printf("Invalid WebRTC WS payload from %s: %v", sessionID, err)
				continue
			}

			if err := validateSignalingMessage(msg); err != nil {
				log.Printf("WebRTC WS dropping payload from %s: %v", sessionID, err)
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			matchedPartner, err := redisClient.GetClient().Get(ctx, appredis.MatchKey(sessionID, sessionRoute)).Result()
			cancel()

			if err != nil || matchedPartner != msg.ToSessionID {
				log.Printf("WebRTC WS unauthorized routing attempt from %s to %s", sessionID, msg.ToSessionID)
				continue
			}

			if msg.Type == "ready" {
				Signaling.MarkReady(sessionID)
				if Signaling.IsReady(msg.ToSessionID) {
					readyPayload := map[string]interface{}{
						"type":    "webrtc_ready",
						"peer_id": msg.ToSessionID,
					}
					readyBytes, _ := json.Marshal(readyPayload)
					_ = Signaling.SendOrQueue(sessionID, readyBytes)

					peerPayload := map[string]interface{}{
						"type":    "webrtc_ready",
						"peer_id": sessionID,
					}
					peerBytes, _ := json.Marshal(peerPayload)
					_ = Signaling.SendOrQueue(msg.ToSessionID, peerBytes)
				}
				continue
			}

			forwardPayload := map[string]interface{}{
				"type":    msg.Type,
				"peer_id": sessionID,
				"data":    msg.Data,
			}

			payloadBytes, _ := json.Marshal(forwardPayload)
			if err := Signaling.SendOrQueue(msg.ToSessionID, payloadBytes); err != nil {
				log.Printf("Failed to route WS signal to %s: %v", msg.ToSessionID, err)
			}
		}

		observability.RecordWebRTCConnectionClosed(c.Request.Context(), time.Since(connectedAt), disconnectReason)
	}
}

func verifySessionTokenWebRTC(ctx context.Context, redisClient redis.UniversalClient, sessionID, providedToken string) bool {
	ctx, span := startChildSpanFromContext(ctx, "webrtc.verify_session_token", hashedAttribute("session.ref", sessionID))
	defer span.End()

	// WebRTC signaling requires the current session route; do not fall back to report locators.
	route, err := appredis.ResolveSessionRoute(ctx, redisClient, sessionID)
	if err != nil {
		span.RecordError(err)
		return false
	}

	expectedToken, err := redisClient.Get(ctx, appredis.SessionTokenKey(sessionID, route)).Result()
	if err != nil || expectedToken == "" {
		if err != nil {
			span.RecordError(err)
		}
		return false
	}

	hash := sha256.Sum256([]byte(providedToken))
	providedHashHex := hex.EncodeToString(hash[:])

	return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(providedHashHex)) == 1
}

func validateSignalingMessage(msg SignalingMessage) error {
	if _, ok := allowedSignalTypes[msg.Type]; !ok {
		return errors.New("unsupported signaling message type")
	}

	if !uuidRe.MatchString(msg.ToSessionID) {
		return errors.New("malformed target session id")
	}

	rawData, err := json.Marshal(msg.Data)
	if err != nil {
		return errors.New("invalid signaling payload")
	}

	if len(rawData) > 32*1024 {
		return errors.New("oversized signaling payload")
	}

	if msg.Type == "ready" {
		return nil
	}

	if len(rawData) <= 2 {
		return errors.New("empty signaling payload")
	}

	return nil
}

func websocketCloseReason(code int) string {
	switch code {
	case websocket.CloseNormalClosure:
		return "normal_closure"
	case websocket.CloseGoingAway:
		return "going_away"
	case websocket.CloseAbnormalClosure:
		return "abnormal_closure"
	case websocket.ClosePolicyViolation:
		return "policy_violation"
	case websocket.CloseMessageTooBig:
		return "message_too_big"
	default:
		return "close_code_" + strconv.Itoa(code)
	}
}
