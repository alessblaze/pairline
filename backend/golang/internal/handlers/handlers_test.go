package handlers

import (
	"strings"
	"testing"
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
