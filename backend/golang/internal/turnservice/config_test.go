package turnservice

import "testing"

func TestParseModeDefaultsToCloudflare(t *testing.T) {
	if got := ParseMode(""); got != ModeCloudflare {
		t.Fatalf("ParseMode(empty) = %q, want %q", got, ModeCloudflare)
	}
	if got := ParseMode("weird"); got != ModeCloudflare {
		t.Fatalf("ParseMode(weird) = %q, want %q", got, ModeCloudflare)
	}
}

func TestBootstrapResponseIntegratedIncludesRelayCredentials(t *testing.T) {
	cfg := Config{
		Mode:        ModeIntegrated,
		STUNServers: []string{"stun:stun.l.google.com:19302"},
		ServerURLs:  []string{"turn:203.0.113.10:3478?transport=udp"},
		Credential:  "pairline-test",
	}

	response := cfgBootstrapResponse(cfg, "session-1", "token-1")
	if response.Mode != ModeIntegrated {
		t.Fatalf("mode = %q, want %q", response.Mode, ModeIntegrated)
	}
	if len(response.IceServers) != 2 {
		t.Fatalf("ice server count = %d, want 2", len(response.IceServers))
	}
	if response.IceServers[1].Username != "session-1|token-1" {
		t.Fatalf("username = %q", response.IceServers[1].Username)
	}
	if response.IceServers[1].Credential != "pairline-test" {
		t.Fatalf("credential = %q", response.IceServers[1].Credential)
	}
}

func TestParseUsername(t *testing.T) {
	sessionID, sessionToken, err := ParseUsername("abc|def")
	if err != nil {
		t.Fatalf("ParseUsername returned error: %v", err)
	}
	if sessionID != "abc" || sessionToken != "def" {
		t.Fatalf("ParseUsername returned %q %q", sessionID, sessionToken)
	}
}

func TestValidateForBootstrapRejectsIntegratedConfigWithoutRelayURLs(t *testing.T) {
	cfg := Config{
		Mode:       ModeIntegrated,
		Realm:      DefaultRealm,
		Credential: DefaultCredential,
	}

	if err := cfg.ValidateForBootstrap(); err == nil {
		t.Fatal("ValidateForBootstrap() error = nil, want error")
	}
}

func TestValidateForBootstrapAcceptsExplicitServerURLs(t *testing.T) {
	cfg := Config{
		Mode:       ModeIntegrated,
		Realm:      DefaultRealm,
		Credential: DefaultCredential,
		ServerURLs: []string{"turn:203.0.113.10:3478?transport=udp"},
	}

	if err := cfg.ValidateForBootstrap(); err != nil {
		t.Fatalf("ValidateForBootstrap() error = %v, want nil", err)
	}
}

func cfgBootstrapResponse(cfg Config, sessionID, sessionToken string) BootstrapResponse {
	return cfg.BootstrapResponse(sessionID, sessionToken)
}
