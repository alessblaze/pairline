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

	"github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 25 * time.Second
	maxMessageSize = 64 * 1024
	maxPendingMsgs = 64
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

type SignalingHub struct {
	sync.RWMutex
	Clients map[string]*SignalingClient
	Pending map[string][][]byte
	Ready   map[string]bool
}

var Hub = &SignalingHub{
	Clients: make(map[string]*SignalingClient),
	Pending: make(map[string][][]byte),
	Ready:   make(map[string]bool),
}

type SignalingMessage struct {
	Type          string                 `json:"type"`            // "offer", "answer", "ice"
	FromSessionID string                 `json:"from_session_id"` // Sender
	ToSessionID   string                 `json:"to_session_id"`   // Recipient
	Data          map[string]interface{} `json:"data"`            // Payload
}

func (h *SignalingHub) Register(sessionID string, conn *websocket.Conn) (*SignalingClient, [][]byte) {
	h.Lock()
	defer h.Unlock()

	client := &SignalingClient{Conn: conn}
	h.Clients[sessionID] = client
	pending := h.Pending[sessionID]
	delete(h.Pending, sessionID)

	return client, pending
}

func (h *SignalingHub) Unregister(sessionID string) {
	h.Lock()
	defer h.Unlock()
	delete(h.Clients, sessionID)
	delete(h.Ready, sessionID)
	delete(h.Pending, sessionID)
}

func (h *SignalingHub) StartBackgroundGC(redisClient *redis.Client) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("CRITICAL: SignalingHub GC panic recovered: %v", r)
			}
		}()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			h.cleanupPending(redisClient)
		}
	}()
}

func (h *SignalingHub) cleanupPending(redisClient *redis.Client) {
	h.Lock()
	var toCheck []string
	for sessionID := range h.Pending {
		toCheck = append(toCheck, sessionID)
	}
	h.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var dead []string
	for _, sessionID := range toCheck {
		exists, err := redisClient.GetClient().Exists(ctx, "session:"+sessionID+":token").Result()
		if err != nil || exists == 0 {
			dead = append(dead, sessionID)
		}
	}

	if len(dead) > 0 {
		h.Lock()
		for _, sessionID := range dead {
			delete(h.Pending, sessionID)
		}
		h.Unlock()
		log.Printf("WebRTC GC pruned %d abandoned incoming signaling queues", len(dead))
	}
}

func (h *SignalingHub) ForwardOrQueue(targetSessionID string, payload []byte) (*SignalingClient, bool) {
	h.Lock()
	defer h.Unlock()

	targetConn, ok := h.Clients[targetSessionID]
	if ok {
		return targetConn, true
	}

	if len(h.Pending[targetSessionID]) >= maxPendingMsgs {
		h.Pending[targetSessionID] = h.Pending[targetSessionID][1:]
	}
	h.Pending[targetSessionID] = append(h.Pending[targetSessionID], payload)
	return nil, false
}

func (h *SignalingHub) MarkReady(sessionID string) {
	h.Lock()
	defer h.Unlock()
	h.Ready[sessionID] = true
}

func (h *SignalingHub) IsReady(sessionID string) bool {
	h.RLock()
	defer h.RUnlock()
	return h.Ready[sessionID]
}

// WebRTCWebSocketHandlerGin upgrades WebRTC signaling to a persistent duplex connection natively in Go.
func WebRTCWebSocketHandlerGin(redisClient *redis.Client) gin.HandlerFunc {
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

		client, pendingMessages := Hub.Register(sessionID, conn)

		defer func() {
			Hub.Unregister(sessionID)
			conn.Close()
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

		// Read loop
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

			// Security matching validation: Ensure from_session_id is actually paired with to_session_id via Redis
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
				Hub.MarkReady(sessionID)
				if Hub.IsReady(msg.ToSessionID) {
					readyPayload := map[string]interface{}{
						"type":    "webrtc_ready",
						"peer_id": msg.ToSessionID,
					}
					readyBytes, _ := json.Marshal(readyPayload)

					currentClient, ok := Hub.ForwardOrQueue(sessionID, readyBytes)
					if ok {
						if err := currentClient.WriteMessage(websocket.TextMessage, readyBytes); err != nil {
							log.Printf("Failed to deliver ready signal to %s: %v", sessionID, err)
						}
					}

					peerPayload := map[string]interface{}{
						"type":    "webrtc_ready",
						"peer_id": sessionID,
					}
					peerBytes, _ := json.Marshal(peerPayload)

					targetConn, ok := Hub.ForwardOrQueue(msg.ToSessionID, peerBytes)
					if ok {
						if err := targetConn.WriteMessage(websocket.TextMessage, peerBytes); err != nil {
							log.Printf("Failed to deliver ready signal to %s: %v", msg.ToSessionID, err)
						}
					}
				}
				continue
			}

			// Format payload exactly as React expects it
			forwardPayload := map[string]interface{}{
				"type":    msg.Type,
				"peer_id": sessionID,
				"data":    msg.Data,
			}

			payloadBytes, _ := json.Marshal(forwardPayload)

			targetConn, ok := Hub.ForwardOrQueue(msg.ToSessionID, payloadBytes)
			if ok {
				if err := targetConn.WriteMessage(websocket.TextMessage, payloadBytes); err != nil {
					log.Printf("Failed to deliver WS signal to %s: %v", msg.ToSessionID, err)
				}
			} else {
				log.Printf("Target %s not found in local WS Hub, queued signal", msg.ToSessionID)
			}
		}
	}
}
