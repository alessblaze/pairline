package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anish/omegle/backend/golang/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestCanCreateAdminRole(t *testing.T) {
	tests := []struct {
		name        string
		currentRole string
		targetRole  string
		want        bool
	}{
		{name: "root can create root", currentRole: "root", targetRole: "root", want: true},
		{name: "root can create admin", currentRole: "root", targetRole: "admin", want: true},
		{name: "root can create moderator", currentRole: "root", targetRole: "moderator", want: true},
		{name: "admin can create moderator", currentRole: "admin", targetRole: "moderator", want: true},
		{name: "admin cannot create admin", currentRole: "admin", targetRole: "admin", want: false},
		{name: "admin cannot create root", currentRole: "admin", targetRole: "root", want: false},
		{name: "moderator cannot create anyone", currentRole: "moderator", targetRole: "moderator", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canCreateAdminRole(tt.currentRole, tt.targetRole); got != tt.want {
				t.Fatalf("canCreateAdminRole(%q, %q) = %v, want %v", tt.currentRole, tt.targetRole, got, tt.want)
			}
		})
	}
}

func TestNormalizeChatLogRejectsOversizedMessage(t *testing.T) {
	raw := []byte(`[{"id":"1","text":"` + strings.Repeat("a", 2001) + `","sender":"me","timestamp":1}]`)
	if _, err := normalizeChatLog(raw); err == nil {
		t.Fatal("normalizeChatLog should reject oversized messages")
	}
}

func TestNormalizeChatLogSanitizesHTML(t *testing.T) {
	raw := []byte(`[{"id":"1","text":"<b>Hello</b>","sender":"peer","timestamp":1}]`)
	normalized, err := normalizeChatLog(raw)
	if err != nil {
		t.Fatalf("normalizeChatLog returned error: %v", err)
	}
	if normalized != `[{"id":"1","text":"Hello","sender":"peer","timestamp":1}]` {
		t.Fatalf("normalizeChatLog() = %s", normalized)
	}
}

func TestIssueAdminSessionIssuesTypedTokens(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"

	accessToken, refreshToken, csrfToken, err := issueAdminSession("alice", "moderator", secret, 15*time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("issueAdminSession returned error: %v", err)
	}

	if csrfToken == "" {
		t.Fatal("issueAdminSession should return a CSRF token")
	}

	if _, _, err := middleware.VerifyJWT(accessToken, secret); err != nil {
		t.Fatalf("VerifyJWT(access) returned error: %v", err)
	}

	if _, _, err := middleware.VerifyJWTWithType(refreshToken, secret, middleware.TokenTypeRefresh); err != nil {
		t.Fatalf("VerifyJWTWithType(refresh) returned error: %v", err)
	}
}

func TestWriteAdminAuthResponseSetsHeaderAndBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	writeAdminAuthResponse(ctx, "alice", "admin", "csrf-123")

	if got := recorder.Header().Get("X-CSRF-Token"); got != "csrf-123" {
		t.Fatalf("X-CSRF-Token header = %q, want %q", got, "csrf-123")
	}

	var body struct {
		Username  string `json:"username"`
		Role      string `json:"role"`
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if body.Username != "alice" || body.Role != "admin" || body.CSRFToken != "csrf-123" {
		t.Fatalf("response body = %+v", body)
	}
}

func TestResolveBanIPAddressFallsBackToRequestedIP(t *testing.T) {
	ip := resolveBanIPAddress(t.Context(), nil, "4f9a0eb6-59fd-4a6f-a10d-c3b91e782a97", "203.0.113.24")
	if ip != "203.0.113.24" {
		t.Fatalf("resolveBanIPAddress() = %q, want %q", ip, "203.0.113.24")
	}
}

func TestDispatchLocalQueuesOutboundMessage(t *testing.T) {
	hub := NewRedisSignalingHub()
	client := newSignalingClient(nil)
	payload := []byte(`{"type":"offer"}`)

	hub.Clients["session-1"] = client

	if ok := hub.dispatchLocal("session-1", payload); !ok {
		t.Fatal("dispatchLocal should queue an outbound message for a local client")
	}

	select {
	case message := <-client.send:
		if message.messageType != websocket.TextMessage {
			t.Fatalf("queued message type = %d, want %d", message.messageType, websocket.TextMessage)
		}
		if string(message.data) != string(payload) {
			t.Fatalf("queued payload = %s, want %s", message.data, payload)
		}
	default:
		t.Fatal("dispatchLocal did not enqueue a message")
	}
}

func TestDispatchLocalDoesNotBlockOnBackedUpClient(t *testing.T) {
	hub := NewRedisSignalingHub()
	client := newSignalingClient(nil)
	hub.Clients["session-1"] = client

	for i := 0; i < cap(client.send); i++ {
		client.send <- outboundMessage{messageType: websocket.TextMessage, data: []byte("queued")}
	}

	start := time.Now()
	if ok := hub.dispatchLocal("session-1", []byte("overflow")); ok {
		t.Fatal("dispatchLocal should reject writes when the client queue is full")
	}

	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("dispatchLocal took %v with a full client queue, want a fast failure", elapsed)
	}

	deadline := time.Now().Add(time.Second)
	for {
		select {
		case <-client.done:
			return
		default:
			if time.Now().After(deadline) {
				t.Fatal("expected backed-up client to be closed")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestRunOwnerRefreshOnceRefreshesClientsInParallel(t *testing.T) {
	hub := NewRedisSignalingHub()

	for i := 0; i < 8; i++ {
		hub.Clients[fmt.Sprintf("session-%d", i)] = newSignalingClient(nil)
	}

	var mu sync.Mutex
	inFlight := 0
	maxInFlight := 0
	calls := 0

	hub.refreshOwnerFunc = func(ctx context.Context, sessionID string) error {
		mu.Lock()
		inFlight++
		calls++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}

		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	}

	hub.runOwnerRefreshOnce()

	if calls != 8 {
		t.Fatalf("refreshOwnerFunc called %d times, want 8", calls)
	}

	if maxInFlight < 2 {
		t.Fatalf("runOwnerRefreshOnce max parallelism = %d, want at least 2", maxInFlight)
	}
}

func TestSignalRateLimiterBlocksMessageFloods(t *testing.T) {
	limiter := newSignalRateLimiter(time.Unix(0, 0))

	allowed := 0
	for i := 0; i < int(signalBurstLimit); i++ {
		if !limiter.allow(time.Unix(0, 0)) {
			t.Fatalf("allow() rejected burst message %d unexpectedly", i)
		}
		allowed++
	}

	if limiter.allow(time.Unix(0, 0)) {
		t.Fatal("allow() should reject a burst above the configured limit")
	}

	refillAt := time.Unix(0, 0).Add(50 * time.Millisecond)
	if !limiter.allow(refillAt) {
		t.Fatal("allow() should permit a message once tokens have refilled")
	}

	if allowed != int(signalBurstLimit) {
		t.Fatalf("allowed burst = %d, want %d", allowed, int(signalBurstLimit))
	}
}
