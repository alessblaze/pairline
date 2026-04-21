package moderation

import (
	"testing"
	"time"

	"github.com/anish/omegle/backend/golang/internal/storage"
)

func TestNormalizeIPUnmapsIPv4Addresses(t *testing.T) {
	if got := normalizeIP(" ::ffff:203.0.113.24 "); got != "203.0.113.24" {
		t.Fatalf("normalizeIP() = %q, want %q", got, "203.0.113.24")
	}
}

func TestSanitizeIPAddressRejectsPrivateAddresses(t *testing.T) {
	if got, ok := sanitizeIPAddress("192.168.1.10"); ok || got != "" {
		t.Fatalf("sanitizeIPAddress(private) = (%q, %v), want (%q, %v)", got, ok, "", false)
	}

	if got, ok := sanitizeIPAddress("203.0.113.24"); !ok || got != "203.0.113.24" {
		t.Fatalf("sanitizeIPAddress(public) = (%q, %v), want (%q, %v)", got, ok, "203.0.113.24", true)
	}
}

func TestIsPrivateOrLocalIPTreatsInvalidInputAsPrivate(t *testing.T) {
	if !isPrivateOrLocalIP("not-an-ip") {
		t.Fatal("isPrivateOrLocalIP() should treat invalid input as non-bannable")
	}

	if !isPrivateOrLocalIP("100.64.1.1") {
		t.Fatal("isPrivateOrLocalIP() should reject CGNAT addresses")
	}
}

func TestRedisBanTTL(t *testing.T) {
	if got := redisBanTTL(storage.Ban{}); got != 0 {
		t.Fatalf("redisBanTTL(no expiry) = %v, want %v", got, 0)
	}

	expiredAt := time.Now().Add(-time.Minute)
	if got := redisBanTTL(storage.Ban{ExpiresAt: &expiredAt}); got != time.Second {
		t.Fatalf("redisBanTTL(expired) = %v, want %v", got, time.Second)
	}

	futureAt := time.Now().Add(5 * time.Minute)
	if got := redisBanTTL(storage.Ban{ExpiresAt: &futureAt}); got <= 0 {
		t.Fatalf("redisBanTTL(future) = %v, want a positive TTL", got)
	}
}
