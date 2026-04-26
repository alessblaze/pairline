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
	"bytes"
	"encoding/json"
	"log"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anish/omegle/backend/golang/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type lockedLogBuffer struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	writeCh chan struct{}
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buffer.Write(p)
	select {
	case b.writeCh <- struct{}{}:
	default:
	}
	return n, err
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

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

func TestParseAdminSettingBoolDefaultsToEnabled(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "", want: true},
		{value: "true", want: true},
		{value: "1", want: true},
		{value: "false", want: false},
		{value: "0", want: false},
		{value: "disabled", want: false},
	}

	for _, tt := range tests {
		if got := parseAdminSettingBool(tt.value); got != tt.want {
			t.Fatalf("parseAdminSettingBool(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestBoolSettingEncoders(t *testing.T) {
	if got := boolToAdminSettingValue(true); got != "true" {
		t.Fatalf("boolToAdminSettingValue(true) = %q", got)
	}
	if got := boolToAdminSettingValue(false); got != "false" {
		t.Fatalf("boolToAdminSettingValue(false) = %q", got)
	}
	if got := BoolToRedisSettingValue(true); got != "1" {
		t.Fatalf("BoolToRedisSettingValue(true) = %q", got)
	}
	if got := BoolToRedisSettingValue(false); got != "0" {
		t.Fatalf("BoolToRedisSettingValue(false) = %q", got)
	}
}

func TestDispatchLocalQueuesOutboundMessage(t *testing.T) {
	hub := NewRedisSignalingHub()
	client := newSignalingClient(nil)
	payload := []byte(`{"type":"offer"}`)

	cs := hub.clientShard("session-1")
	cs.mu.Lock()
	cs.items["session-1"] = &sessionEntry{client: client}
	cs.mu.Unlock()

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
	cs := hub.clientShard("session-1")
	cs.mu.Lock()
	cs.items["session-1"] = &sessionEntry{client: client}
	cs.mu.Unlock()

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

func TestWriteCloseControlNilConnNoop(t *testing.T) {
	if err := writeCloseControl(nil, websocket.ClosePolicyViolation, "already connected"); err != nil {
		t.Fatalf("writeCloseControl(nil) returned error: %v", err)
	}
}

func TestReportAutoApprovalFilterTargetsSpecificReportWhenProvided(t *testing.T) {
	filter, args := reportAutoApprovalFilter(
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"203.0.113.24",
	)

	wantFilter := "id = ? AND (reported_session_id = ? OR reported_ip = ?)"
	if filter != wantFilter {
		t.Fatalf("reportAutoApprovalFilter() filter = %q, want %q", filter, wantFilter)
	}

	wantArgs := []interface{}{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"203.0.113.24",
	}
	if len(args) != len(wantArgs) {
		t.Fatalf("reportAutoApprovalFilter() args length = %d, want %d", len(args), len(wantArgs))
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Fatalf("reportAutoApprovalFilter() args[%d] = %v, want %v", i, args[i], wantArgs[i])
		}
	}
}

func TestReportAutoApprovalFilterFallsBackToRelatedReportsWithoutReportID(t *testing.T) {
	filter, args := reportAutoApprovalFilter(
		"",
		"22222222-2222-2222-2222-222222222222",
		"203.0.113.24",
	)

	wantFilter := "(reported_session_id = ? OR reported_ip = ?)"
	if filter != wantFilter {
		t.Fatalf("reportAutoApprovalFilter() filter = %q, want %q", filter, wantFilter)
	}

	wantArgs := []interface{}{
		"22222222-2222-2222-2222-222222222222",
		"203.0.113.24",
	}
	if len(args) != len(wantArgs) {
		t.Fatalf("reportAutoApprovalFilter() args length = %d, want %d", len(args), len(wantArgs))
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Fatalf("reportAutoApprovalFilter() args[%d] = %v, want %v", i, args[i], wantArgs[i])
		}
	}
}

func TestAsyncEnqueueAutoModerationReturnsWithoutWaiting(t *testing.T) {
	started := make(chan string, 1)
	release := make(chan struct{})

	start := time.Now()
	asyncEnqueueAutoModeration(func(reportID string) {
		started <- reportID
		<-release
	}, "report-123")

	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("asyncEnqueueAutoModeration took %v, want a fast return", elapsed)
	}

	select {
	case got := <-started:
		if got != "report-123" {
			t.Fatalf("asyncEnqueueAutoModeration reportID = %q, want %q", got, "report-123")
		}
	case <-time.After(time.Second):
		t.Fatal("asyncEnqueueAutoModeration did not invoke the enqueue callback")
	}

	close(release)
}

func TestAsyncEnqueueAutoModerationRecoversPanics(t *testing.T) {
	logBuffer := &lockedLogBuffer{writeCh: make(chan struct{}, 1)}
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(logBuffer)
	log.SetFlags(0)
	defer log.SetOutput(originalWriter)
	defer log.SetFlags(originalFlags)

	done := make(chan struct{})
	asyncEnqueueAutoModeration(func(string) {
		defer close(done)
		panic("boom")
	}, "report-123")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("asyncEnqueueAutoModeration did not invoke the enqueue callback")
	}

	select {
	case <-logBuffer.writeCh:
	case <-time.After(time.Second):
		t.Fatal("panic recovery log was not written")
	}

	if !strings.Contains(logBuffer.String(), "auto moderation enqueue panicked report_id=\"report-123\" reason=boom") {
		t.Fatalf("log output = %q, want recovered panic log", logBuffer.String())
	}
}
