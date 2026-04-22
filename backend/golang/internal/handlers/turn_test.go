package handlers

import "testing"

func TestCloudflareTurnCacheKeyVariesByToken(t *testing.T) {
	keyA := cloudflareTurnCacheKey("session-1", "token-a")
	keyB := cloudflareTurnCacheKey("session-1", "token-b")

	if keyA == keyB {
		t.Fatalf("cloudflareTurnCacheKey() reused cache key across token changes: %q", keyA)
	}
}

func TestSummarizeProviderErrorBody(t *testing.T) {
	if got := summarizeProviderErrorBody([]byte("   ")); got != "<empty>" {
		t.Fatalf("summarizeProviderErrorBody(empty) = %q, want %q", got, "<empty>")
	}

	longBody := make([]byte, providerErrorLogLimit+32)
	for i := range longBody {
		longBody[i] = 'a'
	}

	got := summarizeProviderErrorBody(longBody)
	if len(got) != providerErrorLogLimit+3 {
		t.Fatalf("summarizeProviderErrorBody(long) length = %d, want %d", len(got), providerErrorLogLimit+3)
	}
	if got[len(got)-3:] != "..." {
		t.Fatalf("summarizeProviderErrorBody(long) suffix = %q, want %q", got[len(got)-3:], "...")
	}
}

func TestValidateCloudflareCredentialsResponse(t *testing.T) {
	valid := cloudflareCredentialsResponse{
		IceServers: cloudflareIceServersWrapper{
			URLs:       []string{"turn:example.com:3478?transport=udp"},
			Username:   "user",
			Credential: "pass",
		},
	}
	if err := validateCloudflareCredentialsResponse(valid); err != nil {
		t.Fatalf("validateCloudflareCredentialsResponse(valid) error = %v", err)
	}

	tests := []cloudflareCredentialsResponse{
		{IceServers: cloudflareIceServersWrapper{Username: "user", Credential: "pass"}},
		{IceServers: cloudflareIceServersWrapper{URLs: []string{"turn:example.com:3478?transport=udp"}, Credential: "pass"}},
		{IceServers: cloudflareIceServersWrapper{URLs: []string{"turn:example.com:3478?transport=udp"}, Username: "user"}},
	}

	for _, tc := range tests {
		if err := validateCloudflareCredentialsResponse(tc); err == nil {
			t.Fatalf("validateCloudflareCredentialsResponse(%+v) error = nil, want error", tc)
		}
	}
}
