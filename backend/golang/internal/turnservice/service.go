package turnservice

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/turncontrol"
	pionturn "github.com/pion/turn/v5"
)

type Service struct {
	config      Config
	server      *pionturn.Server
	packetConns []net.PacketConn
	listeners   []net.Listener
	allocMu     sync.Mutex
	allocations map[string]int
	validator   Validator
}

type Validator interface {
	ValidateTURNUsername(context.Context, string) (ValidationResult, error)
}

type grpcValidator struct {
	client turncontrol.ServiceClient
}

func (v *grpcValidator) ValidateTURNUsername(ctx context.Context, username string) (ValidationResult, error) {
	resp, err := v.client.ValidateTurnUsername(ctx, &turncontrol.ValidateTurnUsernameRequest{Username: username})
	if err != nil {
		return ValidationResult{}, err
	}
	if !resp.Allowed {
		return ValidationResult{}, validationErrorFromReason(resp.Reason)
	}
	route, err := appredis.DecodeSessionRoute(resp.Route)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("decode turn control route: %w", err)
	}
	return ValidationResult{
		SessionID: resp.SessionID,
		Route:     route,
		MatchedID: resp.MatchedID,
	}, nil
}

func NewService(config Config, validator Validator) (*Service, error) {
	if validator == nil {
		return nil, fmt.Errorf("turn validator is required")
	}
	if err := config.ValidateForRelay(); err != nil {
		return nil, err
	}

	svc := &Service{
		config:      config,
		allocations: make(map[string]int),
		validator:   validator,
	}

	packetConfigs := make([]pionturn.PacketConnConfig, 0, 1)
	listenerConfigs := make([]pionturn.ListenerConfig, 0, 2)

	if config.UDPListenAddress != "" {
		packetConn, err := net.ListenPacket("udp", config.UDPListenAddress)
		if err != nil {
			return nil, fmt.Errorf("listen udp %s: %w", config.UDPListenAddress, err)
		}
		svc.packetConns = append(svc.packetConns, packetConn)
		packetConfigs = append(packetConfigs, pionturn.PacketConnConfig{
			PacketConn:            packetConn,
			RelayAddressGenerator: relayAddressGenerator(config),
		})
	}

	if config.TCPListenAddress != "" {
		listener, err := net.Listen("tcp", config.TCPListenAddress)
		if err != nil {
			svc.closeSockets()
			return nil, fmt.Errorf("listen tcp %s: %w", config.TCPListenAddress, err)
		}
		svc.listeners = append(svc.listeners, listener)
		listenerConfigs = append(listenerConfigs, pionturn.ListenerConfig{
			Listener:              listener,
			RelayAddressGenerator: relayAddressGenerator(config),
		})
	}

	if config.TLSListenAddress != "" {
		certificate, err := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
		if err != nil {
			svc.closeSockets()
			return nil, fmt.Errorf("load TURN TLS certificate: %w", err)
		}

		listener, err := tls.Listen("tcp", config.TLSListenAddress, &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		})
		if err != nil {
			svc.closeSockets()
			return nil, fmt.Errorf("listen tls %s: %w", config.TLSListenAddress, err)
		}
		svc.listeners = append(svc.listeners, listener)
		listenerConfigs = append(listenerConfigs, pionturn.ListenerConfig{
			Listener:              listener,
			RelayAddressGenerator: relayAddressGenerator(config),
		})
	}

	server, err := pionturn.NewServer(pionturn.ServerConfig{
		Realm:             config.Realm,
		PacketConnConfigs: packetConfigs,
		ListenerConfigs:   listenerConfigs,
		AuthHandler:       svc.authHandler,
		QuotaHandler:      svc.quotaHandler,
		EventHandler: pionturn.EventHandler{
			OnAllocationCreated: func(_, _ net.Addr, _, userID, _ string, _ net.Addr, _ int) {
				svc.allocMu.Lock()
				defer svc.allocMu.Unlock()
				svc.allocations[userID]++
			},
			OnAllocationDeleted: func(_, _ net.Addr, _, userID, _ string) {
				svc.allocMu.Lock()
				defer svc.allocMu.Unlock()
				if svc.allocations[userID] > 1 {
					svc.allocations[userID]--
				} else {
					delete(svc.allocations, userID)
				}
			},
		},
	})
	if err != nil {
		svc.closeSockets()
		return nil, fmt.Errorf("create TURN server: %w", err)
	}

	svc.server = server
	return svc, nil
}

func (s *Service) Run(ctx context.Context) error {
	if s == nil || s.server == nil {
		return fmt.Errorf("turn service is not initialized")
	}

	log.Printf(
		"Pairline TURN relay listening udp=%q tcp=%q tls=%q public_ip=%q advertised_urls=%v",
		s.config.UDPListenAddress,
		s.config.TCPListenAddress,
		s.config.TLSListenAddress,
		s.config.PublicIP,
		s.config.AdvertisedTURNURLs(),
	)

	<-ctx.Done()
	return s.Close()
}

func (s *Service) Close() error {
	var errs []error
	if s.server != nil {
		if err := s.server.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.closeSockets(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Service) authHandler(ra *pionturn.RequestAttributes) (string, []byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := s.validator.ValidateTURNUsername(ctx, ra.Username)
	if err != nil {
		log.Printf("TURN auth denied src=%v reason=%v", ra.SrcAddr, err)
		return "", nil, false
	}

	log.Printf("TURN auth allowed session=%s matched=%s src=%v", result.SessionID, result.MatchedID, ra.SrcAddr)
	return ra.Username, pionturn.GenerateAuthKey(ra.Username, ra.Realm, s.config.Credential), true
}

func (s *Service) quotaHandler(username, realm string, srcAddr net.Addr) bool {
	if s.config.AllocationQuota <= 0 {
		return true
	}

	s.allocMu.Lock()
	defer s.allocMu.Unlock()
	return s.allocations[username] < s.config.AllocationQuota
}

func (s *Service) closeSockets() error {
	var errs []error
	closePacketConns := s.packetConns
	closeListeners := s.listeners

	for _, packetConn := range closePacketConns {
		if packetConn != nil {
			if err := packetConn.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, listener := range closeListeners {
		if listener != nil {
			if err := listener.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	s.packetConns = nil
	s.listeners = nil

	return errors.Join(errs...)
}

func relayAddressGenerator(config Config) pionturn.RelayAddressGenerator {
	return &pionturn.RelayAddressGeneratorPortRange{
		RelayAddress: net.ParseIP(config.PublicIP),
		Address:      config.RelayAddress,
		MinPort:      uint16(config.RelayMinPort),
		MaxPort:      uint16(config.RelayMaxPort),
		MaxRetries:   64,
	}
}

func NewGRPCValidator(ctx context.Context, config Config) (Validator, func() error, error) {
	conn, err := turncontrol.NewAuthenticatedClientConn(ctx, config.ControlGRPCAddress, config.ControlGRPCSecret)
	if err != nil {
		return nil, nil, err
	}

	return &grpcValidator{client: turncontrol.NewServiceClient(conn)}, conn.Close, nil
}

func validationErrorFromReason(reason string) error {
	switch reason {
	case "invalid_session_identity":
		return ErrInvalidSessionIdentity
	case "session_not_found":
		return ErrSessionNotFound
	case "session_inactive":
		return ErrSessionInactive
	case "session_unmatched":
		return ErrSessionUnmatched
	case "session_banned":
		return ErrSessionBanned
	default:
		return fmt.Errorf("turn control validation failed: %s", reason)
	}
}
