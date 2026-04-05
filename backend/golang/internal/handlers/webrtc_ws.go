package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var allowedSignalTypes = map[string]struct{}{
	"ready":  {},
	"offer":  {},
	"answer": {},
	"ice":    {},
}

var errSessionAlreadyOwned = errors.New("session signaling already connected elsewhere")

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
	Conn *websocket.Conn
	mu   sync.Mutex
}

func (c *SignalingClient) WriteMessage(messageType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.Conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return c.Conn.WriteMessage(messageType, data)
}

type redisSignalEnvelope struct {
	TargetSessionID string          `json:"target_session_id"`
	Payload         json.RawMessage `json:"payload"`
}

type RedisSignalingHub struct {
	sync.RWMutex
	Clients    map[string]*SignalingClient
	redis      *appredis.Client
	instanceID string
	startOnce  sync.Once
}

var Signaling = NewRedisSignalingHub()

func NewRedisSignalingHub() *RedisSignalingHub {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "go-worker"
	}

	instanceID := hostname + ":" + time.Now().UTC().Format("20060102150405.000000000")

	return &RedisSignalingHub{
		Clients:    make(map[string]*SignalingClient),
		instanceID: instanceID,
	}
}

func (h *RedisSignalingHub) Start(redisClient *appredis.Client) {
	h.startOnce.Do(func() {
		h.redis = redisClient
		go h.runSubscriber()
		go h.runOwnerRefresh()
	})
}

func (h *RedisSignalingHub) Register(sessionID string, conn *websocket.Conn) (*SignalingClient, [][]byte, error) {
	client := &SignalingClient{Conn: conn}

	h.Lock()
	defer h.Unlock()

	if _, exists := h.Clients[sessionID]; exists {
		return nil, nil, errSessionAlreadyOwned
	}

	pending, err := h.claimSession(sessionID)
	if err != nil {
		return nil, nil, err
	}

	h.Clients[sessionID] = client

	return client, pending, nil
}

func (h *RedisSignalingHub) Unregister(sessionID string) {
	h.Lock()
	delete(h.Clients, sessionID)
	h.Unlock()

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
	if h.redis == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	route, err := appredis.ResolveSessionRoute(ctx, h.redis.GetClient(), targetSessionID)
	if err != nil {
		return h.enqueuePending(ctx, targetSessionID, payload)
	}

	owner, err := h.redis.GetClient().Get(ctx, ownerKey(targetSessionID, route)).Result()
	if err == nil && owner != "" {
		envelope, marshalErr := json.Marshal(redisSignalEnvelope{
			TargetSessionID: targetSessionID,
			Payload:         payload,
		})
		if marshalErr == nil {
			delivered, publishErr := h.redis.GetClient().Publish(ctx, signalChannel(owner), envelope).Result()
			if publishErr == nil && delivered > 0 {
				return nil
			}
		}
	}

	return h.enqueuePending(ctx, targetSessionID, payload)
}

func (h *RedisSignalingHub) runSubscriber() {
	for {
		if h.redis == nil {
			time.Sleep(time.Second)
			continue
		}

		ctx := context.Background()
		pubsub := h.redis.GetClient().Subscribe(ctx, signalChannel(h.instanceID))
		_, err := pubsub.Receive(ctx)
		if err != nil {
			log.Printf("Redis signaling subscribe failed for %s: %v", h.instanceID, err)
			_ = pubsub.Close()
			time.Sleep(time.Second)
			continue
		}

		ch := pubsub.Channel()
		log.Printf("Redis signaling subscriber active for %s", h.instanceID)

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
				_ = h.enqueuePending(ctx, envelope.TargetSessionID, envelope.Payload)
				cancel()
			}
		}

		_ = pubsub.Close()
		time.Sleep(time.Second)
	}
}

func (h *RedisSignalingHub) runOwnerRefresh() {
	ticker := time.NewTicker(ownerRefreshEvery)
	defer ticker.Stop()

	for range ticker.C {
		if h.redis == nil {
			continue
		}

		h.RLock()
		sessionIDs := make([]string, 0, len(h.Clients))
		for sessionID := range h.Clients {
			sessionIDs = append(sessionIDs, sessionID)
		}
		h.RUnlock()

		if len(sessionIDs) == 0 {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		for _, sessionID := range sessionIDs {
			if err := h.refreshOwner(ctx, sessionID); err != nil {
				log.Printf("Failed refreshing Redis signaling ownership for %s: %v", sessionID, err)
			}
		}
		cancel()
	}
}

func (h *RedisSignalingHub) dispatchLocal(targetSessionID string, payload []byte) bool {
	h.RLock()
	client, ok := h.Clients[targetSessionID]
	h.RUnlock()

	if !ok {
		return false
	}

	if err := client.WriteMessage(websocket.TextMessage, payload); err != nil {
		log.Printf("Failed to deliver WS signal to %s: %v", targetSessionID, err)
		return false
	}

	return true
}

func (h *RedisSignalingHub) claimSession(sessionID string) ([][]byte, error) {
	if h.redis == nil {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	expectedOwner, takeoverAllowed, err := h.claimPreconditions(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	const claimScript = `
local owner = redis.call("GET", KEYS[1])
if owner and owner ~= ARGV[1] then
  if not (ARGV[5] == "1" and owner == ARGV[4]) then
    return {"owned", owner}
  end
end
redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
redis.call("DEL", KEYS[2])
local pending = redis.call("LRANGE", KEYS[3], 0, tonumber(ARGV[3]) - 1)
redis.call("DEL", KEYS[3])
table.insert(pending, 1, "ok")
return pending
`

	route, err := appredis.ResolveSessionRoute(ctx, h.redis.GetClient(), sessionID)
	if err != nil {
		return nil, err
	}

	result, err := h.redis.GetClient().Eval(
		ctx,
		claimScript,
		[]string{ownerKey(sessionID, route), readyKey(sessionID, route), pendingKey(sessionID, route)},
		h.instanceID,
		int(ownerTTL/time.Second),
		maxPendingMsgs,
		expectedOwner,
		boolToLuaFlag(takeoverAllowed),
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

func (h *RedisSignalingHub) enqueuePending(ctx context.Context, sessionID string, payload []byte) error {
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

func (h *RedisSignalingHub) refreshOwner(ctx context.Context, sessionID string) error {
	const refreshOwnerScript = `
local owner = redis.call("GET", KEYS[1])
if owner == ARGV[1] then
  return redis.call("EXPIRE", KEYS[1], ARGV[2])
end
if not owner then
  redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
  return 1
end
return 0
`
	route, err := appredis.ResolveSessionRoute(ctx, h.redis.GetClient(), sessionID)
	if err != nil {
		return err
	}

	result, err := h.redis.GetClient().Eval(
		ctx,
		refreshOwnerScript,
		[]string{ownerKey(sessionID, route)},
		h.instanceID,
		int(ownerTTL/time.Second),
	).Result()
	if err != nil {
		return err
	}

	switch value := result.(type) {
	case int64:
		if value == 0 {
			return errSessionAlreadyOwned
		}
	case int:
		if value == 0 {
			return errSessionAlreadyOwned
		}
	}

	return nil
}

func (h *RedisSignalingHub) claimPreconditions(ctx context.Context, sessionID string) (string, bool, error) {
	route, err := appredis.ResolveSessionRoute(ctx, h.redis.GetClient(), sessionID)
	if err != nil {
		return "", false, nil
	}

	currentOwner, err := h.redis.GetClient().Get(ctx, ownerKey(sessionID, route)).Result()
	if err == nil {
		if currentOwner == h.instanceID {
			return currentOwner, false, nil
		}

		// Ownership takeover is intentionally conservative in cluster mode.
		// We wait for the TTL or an explicit compare-and-delete path instead of
		// relying on Pub/Sub subscriber counts from Redis.
		return currentOwner, false, nil
	}

	return "", false, nil
}

func boolToLuaFlag(value bool) string {
	if value {
		return "1"
	}
	return "0"
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
	Type          string                 `json:"type"`
	FromSessionID string                 `json:"from_session_id"`
	ToSessionID   string                 `json:"to_session_id"`
	Data          map[string]interface{} `json:"data"`
}

// WebRTCWebSocketHandlerGin upgrades WebRTC signaling to a persistent duplex connection.
func WebRTCWebSocketHandlerGin(redisClient *appredis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID := c.Query("session_id")
		sessionToken := c.Query("session_token")
		if sessionID == "" || sessionToken == "" || len(sessionID) > 100 || len(sessionToken) > 128 || !uuidRe.MatchString(sessionID) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id or session_token format"})
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if !verifySessionToken(ctx, redisClient.GetClient(), sessionID, sessionToken) {
			cancel()
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
			return
		}
		cancel()

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
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
			log.Printf("Failed to register signaling session %s: %v", sessionID, err)
			_ = conn.Close()
			return
		}

		defer func() {
			Signaling.Unregister(sessionID)
			_ = conn.Close()
			log.Printf("WebRTC WS closed for session: %s", sessionID)
		}()

		log.Printf("WebRTC WS registered for session: %s", sessionID)

		done := make(chan struct{})
		defer close(done)

		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("CRITICAL: WebRTC WS ping goroutine panic for session %s: %v", sessionID, r)
				}
			}()
			ticker := time.NewTicker(pingPeriod)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					if err := client.WriteMessage(websocket.PingMessage, nil); err != nil {
						log.Printf("WebRTC WS ping failed for session %s: %v", sessionID, err)
						return
					}
				case <-done:
					return
				}
			}
		}()

		for _, pendingMessage := range pendingMessages {
			if err := client.WriteMessage(websocket.TextMessage, pendingMessage); err != nil {
				log.Printf("Failed delivering queued WS signal to %s: %v", sessionID, err)
				break
			}
		}

		for {
			_, messageData, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebRTC WS connection closed unexpectedly for session: %s", sessionID)
				}
				break
			}

			var msg SignalingMessage
			if err := json.Unmarshal(messageData, &msg); err != nil {
				log.Printf("Invalid WebRTC WS payload from %s: %v", sessionID, err)
				continue
			}

			ownershipCtx, ownershipCancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := Signaling.refreshOwner(ownershipCtx, sessionID); err != nil {
				ownershipCancel()
				if errors.Is(err, errSessionAlreadyOwned) {
					log.Printf("WebRTC WS closing stale signaling owner for session %s", sessionID)
					break
				}
				log.Printf("WebRTC WS could not refresh signaling ownership for %s: %v", sessionID, err)
			} else {
				ownershipCancel()
			}

			if err := validateSignalingMessage(msg); err != nil {
				log.Printf("WebRTC WS dropping payload from %s: %v", sessionID, err)
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			route, routeErr := appredis.ResolveSessionRoute(ctx, redisClient.GetClient(), sessionID)
			if routeErr != nil {
				cancel()
				log.Printf("WebRTC WS missing session route for %s: %v", sessionID, routeErr)
				continue
			}

			matchedPartner, err := redisClient.GetClient().Get(ctx, appredis.MatchKey(sessionID, route)).Result()
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
	}
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
