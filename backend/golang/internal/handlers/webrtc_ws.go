package handlers

import (
	"context"
	"encoding/json"
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
	h.Clients[sessionID] = client
	h.Unlock()

	pending, err := h.claimSession(sessionID)
	if err != nil {
		h.Lock()
		delete(h.Clients, sessionID)
		h.Unlock()
		return nil, nil, err
	}

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

	_ = h.compareAndDelete(ctx, ownerKey(sessionID))
	_ = h.redis.GetClient().Del(ctx, readyKey(sessionID)).Err()
}

func (h *RedisSignalingHub) MarkReady(sessionID string) {
	if h.redis == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = h.redis.GetClient().Set(ctx, readyKey(sessionID), "1", readySignalTTL).Err()
}

func (h *RedisSignalingHub) IsReady(sessionID string) bool {
	if h.redis == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	exists, err := h.redis.GetClient().Exists(ctx, readyKey(sessionID)).Result()
	return err == nil && exists > 0
}

func (h *RedisSignalingHub) SendOrQueue(targetSessionID string, payload []byte) error {
	if h.redis == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	owner, err := h.redis.GetClient().Get(ctx, ownerKey(targetSessionID)).Result()
	if err == nil && owner != "" {
		envelope, marshalErr := json.Marshal(redisSignalEnvelope{
			TargetSessionID: targetSessionID,
			Payload:         payload,
		})
		if marshalErr == nil {
			if publishErr := h.redis.GetClient().Publish(ctx, signalChannel(owner), envelope).Err(); publishErr == nil {
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
		pipe := h.redis.GetClient().Pipeline()
		for _, sessionID := range sessionIDs {
			pipe.Set(ctx, ownerKey(sessionID), h.instanceID, ownerTTL)
		}
		_, err := pipe.Exec(ctx)
		cancel()

		if err != nil {
			log.Printf("Failed refreshing Redis signaling ownership: %v", err)
		}
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

	pipe := h.redis.GetClient().TxPipeline()
	pipe.Set(ctx, ownerKey(sessionID), h.instanceID, ownerTTL)
	pipe.Del(ctx, readyKey(sessionID))
	pendingCmd := pipe.LRange(ctx, pendingKey(sessionID), 0, maxPendingMsgs-1)
	pipe.Del(ctx, pendingKey(sessionID))

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}

	rawPending, err := pendingCmd.Result()
	if err != nil {
		return nil, err
	}

	pending := make([][]byte, 0, len(rawPending))
	for _, item := range rawPending {
		pending = append(pending, []byte(item))
	}

	return pending, nil
}

func (h *RedisSignalingHub) enqueuePending(ctx context.Context, sessionID string, payload []byte) error {
	pipe := h.redis.GetClient().TxPipeline()
	pipe.RPush(ctx, pendingKey(sessionID), string(payload))
	pipe.LTrim(ctx, pendingKey(sessionID), -maxPendingMsgs, -1)
	pipe.Expire(ctx, pendingKey(sessionID), pendingSignalTTL)
	_, err := pipe.Exec(ctx)
	return err
}

func (h *RedisSignalingHub) compareAndDelete(ctx context.Context, key string) error {
	const compareDeleteScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`
	return h.redis.GetClient().Eval(ctx, compareDeleteScript, []string{key}, h.instanceID).Err()
}

func ownerKey(sessionID string) string {
	return "webrtc:owner:" + sessionID
}

func readyKey(sessionID string) string {
	return "webrtc:ready:" + sessionID
}

func pendingKey(sessionID string) string {
	return "webrtc:pending:" + sessionID
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
		if sessionID == "" || sessionToken == "" || len(sessionID) > 100 || len(sessionToken) > 128 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id or session_token format"})
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if !verifySessionToken(ctx, redisClient, sessionID, sessionToken) {
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

			if !uuidRe.MatchString(msg.ToSessionID) {
				log.Printf("WebRTC WS dropping payload due to malformed ToSessionID from %s", sessionID)
				continue
			}

			if len(msg.Data) > 32768 {
				log.Printf("WebRTC WS dropping oversized payload from %s", sessionID)
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			matchedPartner, err := redisClient.GetClient().Get(ctx, "match:"+sessionID).Result()
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
