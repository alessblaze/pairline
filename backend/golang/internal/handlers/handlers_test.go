package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anish/omegle/backend/golang/internal/middleware"
	"github.com/gin-gonic/gin"
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
