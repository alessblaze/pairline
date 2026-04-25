package turnservice

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

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
	expectedHash := sha256.Sum256([]byte("token-1"))
	expectedUsername := "session-1|" + hex.EncodeToString(expectedHash[:])
	if response.IceServers[1].Username != expectedUsername {
		t.Fatalf("username = %q", response.IceServers[1].Username)
	}
	if response.IceServers[1].Credential != "pairline-test" {
		t.Fatalf("credential = %q", response.IceServers[1].Credential)
	}
}

func TestParseUsername(t *testing.T) {
	tokenHash := sha256.Sum256([]byte("def"))
	username := "abc|" + hex.EncodeToString(tokenHash[:])
	sessionID, tokenDigest, err := ParseUsername(username)
	if err != nil {
		t.Fatalf("ParseUsername returned error: %v", err)
	}
	if sessionID != "abc" || tokenDigest != hex.EncodeToString(tokenHash[:]) {
		t.Fatalf("ParseUsername returned %q %q", sessionID, tokenDigest)
	}
}

func TestBuildUsernameDoesNotExposeRawToken(t *testing.T) {
	username := BuildUsername("session-1", "token-1")
	if strings.Contains(username, "token-1") {
		t.Fatalf("BuildUsername() leaked raw token in %q", username)
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

func TestValidateForRelayRejectsServerURLsWithoutListeners(t *testing.T) {
	cfg := Config{
		Mode:         ModeIntegrated,
		Realm:        DefaultRealm,
		Credential:   DefaultCredential,
		PublicIP:     "203.0.113.10",
		ServerURLs:   []string{"turn:203.0.113.10:3478?transport=udp"},
		RelayMinPort: 49152,
		RelayMaxPort: 49252,
	}

	if err := cfg.ValidateForRelay(); err == nil {
		t.Fatal("ValidateForRelay() error = nil, want listener validation error")
	}
}

func TestValidateForRelayRejectsPortRangeAboveUint16(t *testing.T) {
	cfg := Config{
		Mode:             ModeIntegrated,
		Realm:            DefaultRealm,
		Credential:       DefaultCredential,
		PublicIP:         "203.0.113.10",
		UDPListenAddress: ":3478",
		RelayMinPort:     49152,
		RelayMaxPort:     70000,
	}

	if err := cfg.ValidateForRelay(); err == nil {
		t.Fatal("ValidateForRelay() error = nil, want invalid relay port range error")
	}
}

func TestLoadConfigFromEnvDefaultsControlGRPCPoolSize(t *testing.T) {
	t.Setenv("TURN_CONTROL_GRPC_POOL_SIZE", "")

	cfg := LoadConfigFromEnv()
	if cfg.ControlGRPCPoolSize != 4 {
		t.Fatalf("ControlGRPCPoolSize = %d, want 4", cfg.ControlGRPCPoolSize)
	}
}

func cfgBootstrapResponse(cfg Config, sessionID, sessionToken string) BootstrapResponse {
	return cfg.BootstrapResponse(sessionID, sessionToken)
}
