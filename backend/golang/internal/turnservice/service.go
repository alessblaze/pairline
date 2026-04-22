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

	pionturn "github.com/pion/turn/v5"

	appredis "github.com/anish/omegle/backend/golang/internal/redis"
)

type Service struct {
	config      Config
	redis       *appredis.Client
	server      *pionturn.Server
	packetConns []net.PacketConn
	listeners   []net.Listener
	allocMu     sync.Mutex
	allocations map[string]int
}

func NewService(config Config, redisClient *appredis.Client) (*Service, error) {
	if redisClient == nil {
		return nil, fmt.Errorf("redis client is required")
	}
	if err := config.ValidateForRelay(); err != nil {
		return nil, err
	}

	svc := &Service{
		config:      config,
		redis:       redisClient,
		allocations: make(map[string]int),
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

	result, err := ValidateTURNUsername(ctx, s.redis.GetClient(), ra.Username)
	if err != nil {
		log.Printf("TURN auth denied user=%q src=%v reason=%v", ra.Username, ra.SrcAddr, err)
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
