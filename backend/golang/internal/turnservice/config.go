package turnservice

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

type Mode string

const (
	ModeOff        Mode = "off"
	ModeCloudflare Mode = "cloudflare"
	ModeIntegrated Mode = "integrated"
)

const DefaultCredential = "pairline-turn-session"
const DefaultRealm = "pairline"
const integratedUsernameSeparator = "|"

type Config struct {
	Mode                Mode
	Realm               string
	STUNServers         []string
	ServerURLs          []string
	Credential          string
	PublicIP            string
	RelayAddress        string
	UDPListenAddress    string
	TCPListenAddress    string
	TLSListenAddress    string
	HealthListenAddress string
	TLSCertFile         string
	TLSKeyFile          string
	RelayMinPort        int
	RelayMaxPort        int
	AllocationQuota     int
}

type IceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type BootstrapResponse struct {
	Mode       Mode        `json:"mode"`
	IceServers []IceServer `json:"iceServers"`
}

func LoadConfigFromEnv() Config {
	cfg := Config{
		Mode:                ParseMode(os.Getenv("TURN_MODE")),
		Realm:               envOrDefault("TURN_REALM", DefaultRealm),
		STUNServers:         parseCSV(envOrDefault("TURN_STUN_SERVERS", "stun:stun.cloudflare.com:3478,stun:stun.l.google.com:19302")),
		ServerURLs:          parseCSV(os.Getenv("TURN_SERVER_URLS")),
		Credential:          envOrDefault("TURN_STATIC_AUTH_SECRET", DefaultCredential),
		PublicIP:            strings.TrimSpace(os.Getenv("TURN_PUBLIC_IP")),
		RelayAddress:        envOrDefault("TURN_RELAY_ADDRESS", "0.0.0.0"),
		UDPListenAddress:    strings.TrimSpace(os.Getenv("TURN_UDP_LISTEN_ADDRESS")),
		TCPListenAddress:    strings.TrimSpace(os.Getenv("TURN_TCP_LISTEN_ADDRESS")),
		TLSListenAddress:    strings.TrimSpace(os.Getenv("TURN_TLS_LISTEN_ADDRESS")),
		HealthListenAddress: strings.TrimSpace(os.Getenv("TURN_HEALTH_LISTEN_ADDRESS")),
		TLSCertFile:         strings.TrimSpace(os.Getenv("TURN_TLS_CERT_FILE")),
		TLSKeyFile:          strings.TrimSpace(os.Getenv("TURN_TLS_KEY_FILE")),
		RelayMinPort:        envIntOrDefault("TURN_RELAY_MIN_PORT", 49152),
		RelayMaxPort:        envIntOrDefault("TURN_RELAY_MAX_PORT", 49252),
		AllocationQuota:     envIntOrDefault("TURN_MAX_ALLOCATIONS_PER_SESSION", 4),
	}

	if cfg.UDPListenAddress == "" {
		cfg.UDPListenAddress = ":3478"
	}

	return cfg
}

func ParseMode(raw string) Mode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(ModeCloudflare):
		return ModeCloudflare
	case string(ModeIntegrated):
		return ModeIntegrated
	case string(ModeOff):
		return ModeOff
	default:
		return ModeCloudflare
	}
}

func (c Config) ValidateForRelay() error {
	if c.Mode != ModeIntegrated {
		return fmt.Errorf("turn relay requires TURN_MODE=%s", ModeIntegrated)
	}
	if net.ParseIP(c.PublicIP) == nil {
		return fmt.Errorf("TURN_PUBLIC_IP must be set to a valid IP when TURN_MODE=%s", ModeIntegrated)
	}
	if c.Realm == "" {
		return fmt.Errorf("TURN_REALM is required")
	}
	if c.Credential == "" {
		return fmt.Errorf("TURN_STATIC_AUTH_SECRET must not be empty")
	}
	if c.RelayMinPort <= 0 || c.RelayMaxPort <= 0 || c.RelayMinPort > c.RelayMaxPort {
		return fmt.Errorf("invalid TURN relay port range %d-%d", c.RelayMinPort, c.RelayMaxPort)
	}
	if c.TLSListenAddress != "" && (c.TLSCertFile == "" || c.TLSKeyFile == "") {
		return fmt.Errorf("TURN_TLS_CERT_FILE and TURN_TLS_KEY_FILE are required when TURN_TLS_LISTEN_ADDRESS is set")
	}
	if len(c.AdvertisedTURNURLs()) == 0 {
		return fmt.Errorf("no TURN listener is configured")
	}
	return nil
}

func (c Config) ValidateForBootstrap() error {
	if c.Mode != ModeIntegrated {
		return nil
	}
	if c.Realm == "" {
		return fmt.Errorf("TURN_REALM is required")
	}
	if c.Credential == "" {
		return fmt.Errorf("TURN_STATIC_AUTH_SECRET must not be empty")
	}
	if len(c.AdvertisedTURNURLs()) == 0 {
		return fmt.Errorf("integrated TURN bootstrap requires TURN_SERVER_URLS or TURN_PUBLIC_IP with an enabled TURN listener")
	}
	return nil
}

func (c Config) BootstrapResponse(sessionID, sessionToken string) BootstrapResponse {
	iceServers := make([]IceServer, 0, len(c.STUNServers)+1)
	for _, server := range c.STUNServers {
		iceServers = append(iceServers, IceServer{URLs: []string{server}})
	}

	if c.Mode == ModeIntegrated {
		iceServers = append(iceServers, IceServer{
			URLs:       c.AdvertisedTURNURLs(),
			Username:   BuildUsername(sessionID, sessionToken),
			Credential: c.Credential,
		})
	}

	return BootstrapResponse{
		Mode:       c.Mode,
		IceServers: iceServers,
	}
}

func (c Config) AdvertisedTURNURLs() []string {
	if len(c.ServerURLs) > 0 {
		return append([]string(nil), c.ServerURLs...)
	}

	publicHost := strings.TrimSpace(c.PublicIP)
	if publicHost == "" {
		return nil
	}

	urls := make([]string, 0, 3)
	if c.UDPListenAddress != "" {
		if port := extractPort(c.UDPListenAddress, "3478"); port != "" {
			urls = append(urls, "turn:"+net.JoinHostPort(publicHost, port)+"?transport=udp")
		}
	}
	if c.TCPListenAddress != "" {
		if port := extractPort(c.TCPListenAddress, "3478"); port != "" {
			urls = append(urls, "turn:"+net.JoinHostPort(publicHost, port)+"?transport=tcp")
		}
	}
	if c.TLSListenAddress != "" {
		if port := extractPort(c.TLSListenAddress, "5349"); port != "" {
			urls = append(urls, "turns:"+net.JoinHostPort(publicHost, port)+"?transport=tcp")
		}
	}

	return urls
}

func BuildUsername(sessionID, sessionToken string) string {
	return sessionID + integratedUsernameSeparator + sessionToken
}

func ParseUsername(username string) (string, string, error) {
	parts := strings.SplitN(username, integratedUsernameSeparator, 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid integrated TURN username")
	}

	sessionID := strings.TrimSpace(parts[0])
	sessionToken := strings.TrimSpace(parts[1])
	if sessionID == "" || sessionToken == "" {
		return "", "", fmt.Errorf("invalid integrated TURN username")
	}

	return sessionID, sessionToken, nil
}

func parseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}

	return values
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envIntOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func extractPort(address, fallback string) string {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return ""
	}

	if strings.HasPrefix(trimmed, ":") {
		return strings.TrimPrefix(trimmed, ":")
	}

	_, port, err := net.SplitHostPort(trimmed)
	if err != nil {
		return fallback
	}

	return port
}
