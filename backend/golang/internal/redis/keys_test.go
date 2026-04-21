package redis

import (
	"errors"
	"testing"
)

func TestDecodeSessionRouteParsesValidLocator(t *testing.T) {
	route, err := DecodeSessionRoute("video|7")
	if err != nil {
		t.Fatalf("DecodeSessionRoute() returned error: %v", err)
	}

	if route.Mode != "video" || route.Shard != 7 {
		t.Fatalf("DecodeSessionRoute() = %#v, want mode=%q shard=%d", route, "video", 7)
	}
}

func TestDecodeSessionRouteRejectsInvalidLocator(t *testing.T) {
	tests := []string{
		"",
		"video",
		"audio|1",
		"text|-1",
		"text|not-a-number",
	}

	for _, input := range tests {
		if _, err := DecodeSessionRoute(input); !errors.Is(err, ErrInvalidSessionRoute) {
			t.Fatalf("DecodeSessionRoute(%q) error = %v, want %v", input, err, ErrInvalidSessionRoute)
		}
	}
}

func TestSessionRouteDerivedKeys(t *testing.T) {
	route := SessionRoute{Mode: "text", Shard: 3}

	if got := route.Tag(); got != "{text:3}" {
		t.Fatalf("Tag() = %q, want %q", got, "{text:3}")
	}
	if got := SessionDataKey("abc", route); got != "session:{text:3}:data:abc" {
		t.Fatalf("SessionDataKey() = %q", got)
	}
	if got := SessionIPKey("abc", route); got != "session:{text:3}:ip:abc" {
		t.Fatalf("SessionIPKey() = %q", got)
	}
	if got := SessionTokenKey("abc", route); got != "session:{text:3}:token:abc" {
		t.Fatalf("SessionTokenKey() = %q", got)
	}
	if got := MatchKey("abc", route); got != "match:{text:3}:abc" {
		t.Fatalf("MatchKey() = %q", got)
	}
	if got := RecentMatchKey("abc", route); got != "recent_match:{text:3}:abc" {
		t.Fatalf("RecentMatchKey() = %q", got)
	}
	if got := WebRTCOwnerKey("abc", route); got != "webrtc:{text:3}:owner:abc" {
		t.Fatalf("WebRTCOwnerKey() = %q", got)
	}
	if got := WebRTCReadyKey("abc", route); got != "webrtc:{text:3}:ready:abc" {
		t.Fatalf("WebRTCReadyKey() = %q", got)
	}
	if got := WebRTCPendingKey("abc", route); got != "webrtc:{text:3}:pending:abc" {
		t.Fatalf("WebRTCPendingKey() = %q", got)
	}
}
