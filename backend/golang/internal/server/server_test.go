package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anish/omegle/backend/golang/internal/middleware"
	"github.com/gin-gonic/gin"
)

func TestAllowedOriginsFromEnv(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "https://app.example.com, https://admin.example.com ,")

	got := allowedOriginsFromEnv()
	if len(got) != 2 || got[0] != "https://app.example.com" || got[1] != "https://admin.example.com" {
		t.Fatalf("allowedOriginsFromEnv() = %#v", got)
	}
}

func TestOriginAllowedAcceptsTrailingSlashVariants(t *testing.T) {
	allowed := []string{"https://app.example.com"}

	if !originAllowed("https://app.example.com", allowed) {
		t.Fatal("originAllowed() should accept exact matches")
	}
	if !originAllowed("https://app.example.com/", allowed) {
		t.Fatal("originAllowed() should accept origin values with a trailing slash")
	}
	if originAllowed("https://evil.example.com", allowed) {
		t.Fatal("originAllowed() should reject unexpected origins")
	}
}

func TestTrustedProxyCIDRsFromEnv(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	got := trustedProxyCIDRsFromEnv()
	if len(got) != 2 || got[0] != "127.0.0.1/32" || got[1] != "::1/128" {
		t.Fatalf("trustedProxyCIDRsFromEnv(default) = %#v", got)
	}

	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 192.168.0.0/16")
	got = trustedProxyCIDRsFromEnv()
	if len(got) != 2 || got[0] != "10.0.0.0/8" || got[1] != "192.168.0.0/16" {
		t.Fatalf("trustedProxyCIDRsFromEnv(custom) = %#v", got)
	}
}

func TestAdminCSRFMiddlewareAllowsSafeMethods(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/reports", nil)

	calledNext := false
	AdminCSRFMiddleware([]string{"https://app.example.com"})(ctx)
	if !ctx.IsAborted() {
		calledNext = true
	}

	if !calledNext {
		t.Fatal("AdminCSRFMiddleware() should allow safe methods without CSRF headers")
	}
}

func TestAdminCSRFMiddlewareRejectsMismatchedTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/reports", nil)
	ctx.Request.Header.Set("Origin", "https://app.example.com")
	ctx.Request.Header.Set("X-CSRF-Token", "header-token")
	ctx.Request.AddCookie(&http.Cookie{Name: middleware.AdminCSRFCookieName, Value: "cookie-token"})

	AdminCSRFMiddleware([]string{"https://app.example.com"})(ctx)

	if !ctx.IsAborted() {
		t.Fatal("AdminCSRFMiddleware() should abort when tokens do not match")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}
