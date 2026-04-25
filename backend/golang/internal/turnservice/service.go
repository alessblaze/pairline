package turnservice

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anish/omegle/backend/golang/internal/observability"
	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/turncontrol"
	pionturn "github.com/pion/turn/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

var turnTracer = otel.Tracer("pairline/turn")

const releaseAttemptTimeout = 750 * time.Millisecond
const releaseRepairInterval = 2 * time.Second

type PendingRelease struct {
	Username    string
	OperationID string
}

type pendingReleaseState struct {
	PendingRelease
	local   bool
	durable bool
}

type Service struct {
	config                  Config
	server                  *pionturn.Server
	packetConns             []net.PacketConn
	listeners               []net.Listener
	validator               Validator
	mu                      sync.Mutex
	activeAllocations       map[string]activeAllocation
	allocationReleaseLookup map[string]activeAllocation
	allocationKeysByUserID  map[string]map[string]struct{}
	allocationKeysBySession map[string]map[string]struct{}
	allocationCountByUserID map[string]int
	sessionIPByUserID       map[string]rememberedSessionIP
	pendingReleases         map[string]map[string]struct{}
	releaseSignal           chan struct{}
	releaseSequence         atomic.Uint64
	revokeAllocation        func(activeAllocation) error
}

type Validator interface {
	ValidateTURNUsername(context.Context, string) (ValidationResult, error)
	CheckBannedSessionIPs(context.Context, []string) ([]string, error)
	ReserveTURNAllocation(context.Context, string, int) (bool, error)
	ReleaseTURNAllocation(context.Context, string, string) error
	QueuePendingTURNRelease(context.Context, string, string) error
	PendingTURNReleases(context.Context, string) ([]PendingRelease, error)
	CompletePendingTURNRelease(context.Context, string, string) error
}

type grpcValidator struct {
	client turncontrol.ServiceClient
}

type pooledGRPCValidator struct {
	clients []turncontrol.ServiceClient
	next    atomic.Uint64
}

func (v *grpcValidator) ValidateTURNUsername(ctx context.Context, username string) (ValidationResult, error) {
	resp, err := v.client.ValidateTurnUsername(ctx, &turncontrol.ValidateTurnUsernameRequest{Username: username})
	if err != nil {
		return ValidationResult{}, err
	}
	if !resp.Allowed {
		return ValidationResult{}, validationErrorWithSessionIP(validationErrorFromReason(resp.Reason), resp.SessionIP)
	}
	route, err := appredis.DecodeSessionRoute(resp.Route)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("decode turn control route: %w", err)
	}
	return ValidationResult{
		SessionID: resp.SessionID,
		Route:     route,
		MatchedID: resp.MatchedID,
		SessionIP: resp.SessionIP,
	}, nil
}

func (v *grpcValidator) ReserveTURNAllocation(ctx context.Context, username string, limit int) (bool, error) {
	resp, err := v.client.ReserveAllocation(ctx, &turncontrol.ReserveAllocationRequest{
		Username: username,
		Limit:    int32(limit),
	})
	if err != nil {
		return false, err
	}
	if !resp.Allowed {
		if resp.Reason == "" {
			return false, nil
		}
		return false, validationErrorFromReason(resp.Reason)
	}
	return true, nil
}

func (v *grpcValidator) CheckBannedSessionIPs(ctx context.Context, sessionIPs []string) ([]string, error) {
	resp, err := v.client.CheckBannedSessionIPs(ctx, &turncontrol.CheckBannedSessionIPsRequest{SessionIPs: sessionIPs})
	if err != nil {
		return nil, err
	}
	return append([]string(nil), resp.BannedIPs...), nil
}

func (v *grpcValidator) ReleaseTURNAllocation(ctx context.Context, username, operationID string) error {
	resp, err := v.client.ReleaseAllocation(ctx, &turncontrol.ReleaseAllocationRequest{
		Username:    username,
		OperationID: operationID,
	})
	if err != nil {
		return err
	}
	if !resp.Released && resp.Reason != "" {
		return validationErrorFromReason(resp.Reason)
	}
	return nil
}

func (v *grpcValidator) QueuePendingTURNRelease(ctx context.Context, username, operationID string) error {
	resp, err := v.client.QueuePendingRelease(ctx, &turncontrol.QueuePendingReleaseRequest{
		Username:    username,
		OperationID: operationID,
	})
	if err != nil {
		return err
	}
	if !resp.Queued && resp.Reason != "" {
		return validationErrorFromReason(resp.Reason)
	}
	return nil
}

func (v *grpcValidator) PendingTURNReleases(ctx context.Context, username string) ([]PendingRelease, error) {
	resp, err := v.client.PendingReleases(ctx, &turncontrol.PendingReleasesRequest{Username: username})
	if err != nil {
		return nil, err
	}
	releases := make([]PendingRelease, 0, len(resp.Releases))
	for _, release := range resp.Releases {
		releases = append(releases, PendingRelease{
			Username:    release.Username,
			OperationID: release.OperationID,
		})
	}
	return releases, nil
}

func (v *grpcValidator) CompletePendingTURNRelease(ctx context.Context, username, operationID string) error {
	resp, err := v.client.CompletePendingRelease(ctx, &turncontrol.CompletePendingReleaseRequest{
		Username:    username,
		OperationID: operationID,
	})
	if err != nil {
		return err
	}
	if !resp.Completed && resp.Reason != "" {
		return validationErrorFromReason(resp.Reason)
	}
	return nil
}

func (v *pooledGRPCValidator) pickClient() turncontrol.ServiceClient {
	if len(v.clients) == 1 {
		return v.clients[0]
	}

	index := v.next.Add(1) - 1
	return v.clients[index%uint64(len(v.clients))]
}

func (v *pooledGRPCValidator) ValidateTURNUsername(ctx context.Context, username string) (ValidationResult, error) {
	return (&grpcValidator{client: v.pickClient()}).ValidateTURNUsername(ctx, username)
}

func (v *pooledGRPCValidator) ReserveTURNAllocation(ctx context.Context, username string, limit int) (bool, error) {
	return (&grpcValidator{client: v.pickClient()}).ReserveTURNAllocation(ctx, username, limit)
}

func (v *pooledGRPCValidator) CheckBannedSessionIPs(ctx context.Context, sessionIPs []string) ([]string, error) {
	return (&grpcValidator{client: v.pickClient()}).CheckBannedSessionIPs(ctx, sessionIPs)
}

func (v *pooledGRPCValidator) ReleaseTURNAllocation(ctx context.Context, username, operationID string) error {
	return (&grpcValidator{client: v.pickClient()}).ReleaseTURNAllocation(ctx, username, operationID)
}

func (v *pooledGRPCValidator) QueuePendingTURNRelease(ctx context.Context, username, operationID string) error {
	return (&grpcValidator{client: v.pickClient()}).QueuePendingTURNRelease(ctx, username, operationID)
}

func (v *pooledGRPCValidator) PendingTURNReleases(ctx context.Context, username string) ([]PendingRelease, error) {
	return (&grpcValidator{client: v.pickClient()}).PendingTURNReleases(ctx, username)
}

func (v *pooledGRPCValidator) CompletePendingTURNRelease(ctx context.Context, username, operationID string) error {
	return (&grpcValidator{client: v.pickClient()}).CompletePendingTURNRelease(ctx, username, operationID)
}

func NewService(config Config, validator Validator) (*Service, error) {
	if validator == nil {
		return nil, fmt.Errorf("turn validator is required")
	}
	if err := config.ValidateForRelay(); err != nil {
		return nil, err
	}

	svc := &Service{
		config:                  config,
		validator:               validator,
		activeAllocations:       make(map[string]activeAllocation),
		allocationReleaseLookup: make(map[string]activeAllocation),
		allocationKeysByUserID:  make(map[string]map[string]struct{}),
		allocationKeysBySession: make(map[string]map[string]struct{}),
		allocationCountByUserID: make(map[string]int),
		sessionIPByUserID:       make(map[string]rememberedSessionIP),
		pendingReleases:         make(map[string]map[string]struct{}),
		releaseSignal:           make(chan struct{}, 1),
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
			OnAllocationCreated: func(srcAddr, dstAddr net.Addr, protocol, userID, _ string, _ net.Addr, _ int) {
				svc.trackActiveAllocation(srcAddr, dstAddr, protocol, userID)
			},
			OnAllocationDeleted: func(srcAddr, dstAddr net.Addr, protocol, userID, _ string) {
				svc.handleAllocationDeleted(srcAddr, dstAddr, protocol, userID)
			},
		},
	})
	if err != nil {
		svc.closeSockets()
		return nil, fmt.Errorf("create TURN server: %w", err)
	}

	svc.server = server
	svc.revokeAllocation = func(allocation activeAllocation) error {
		return revokeTurnAllocation(svc.server, allocation)
	}
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

	go s.runBanSweep(ctx)
	go s.runReleaseWorker(ctx)
	s.signalReleaseWorker()

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

	startedAt := time.Now()
	ctx, span := turnTracer.Start(ctx, "turn.auth",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("turn.username", ra.Username),
			attribute.String("turn.src_addr", formatAddr(ra.SrcAddr)),
		),
	)
	defer span.End()

	result, err := s.validator.ValidateTURNUsername(ctx, ra.Username)
	if err != nil {
		sessionIP := ValidationErrorSessionIP(err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String("turn.auth.denial_reason", err.Error()))
		if sessionIP != "" {
			span.SetAttributes(attribute.String("turn.session_ip", sessionIP))
		}
		observability.RecordTURNRelayAuth(ctx, time.Since(startedAt), false, err.Error())
		if sessionIP != "" {
			log.Printf("TURN auth denied peer_addr=%v session_ip=%s reason=%v", ra.SrcAddr, sessionIP, err)
		} else {
			log.Printf("TURN auth denied peer_addr=%v reason=%v", ra.SrcAddr, err)
		}
		return "", nil, false
	}

	span.SetAttributes(
		attribute.String("turn.session_id", result.SessionID),
		attribute.String("turn.matched_id", result.MatchedID),
	)
	if result.SessionIP != "" {
		s.rememberSessionIP(ra.Username, result.SessionIP)
		span.SetAttributes(attribute.String("turn.session_ip", result.SessionIP))
	}
	observability.RecordTURNRelayAuth(ctx, time.Since(startedAt), true, "")
	s.logAuthAllowed(result, ra.SrcAddr)
	return ra.Username, pionturn.GenerateAuthKey(ra.Username, ra.Realm, s.config.Credential), true
}

func (s *Service) quotaHandler(username, realm string, srcAddr net.Addr) bool {
	if s.config.AllocationQuota <= 0 {
		return true
	}

	ctx, span := turnTracer.Start(context.Background(), "turn.quota.reserve",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("turn.username", username),
			attribute.String("turn.src_addr", formatAddr(srcAddr)),
			attribute.Int("turn.allocation_quota", s.config.AllocationQuota),
		),
	)
	defer span.End()

	flushCtx, flushCancel := context.WithTimeout(ctx, 2*time.Second)
	defer flushCancel()
	if err := s.flushPendingReleases(flushCtx, username); err != nil {
		span.RecordError(err)
		log.Printf("TURN pending release flush failed user=%q peer_addr=%v reason=%v", username, srcAddr, err)
	}

	reserveCtx, reserveCancel := context.WithTimeout(ctx, 2*time.Second)
	defer reserveCancel()
	allowed, err := s.validator.ReserveTURNAllocation(reserveCtx, username, s.config.AllocationQuota)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		observability.RecordTURNRelayQuota(ctx, false)
		log.Printf("TURN allocation quota validation failed user=%q peer_addr=%v reason=%v", username, srcAddr, err)
		return false
	}

	span.SetAttributes(attribute.Bool("turn.quota.allowed", allowed))
	observability.RecordTURNRelayQuota(ctx, allowed)
	return allowed
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
	poolSize := config.ControlGRPCPoolSize
	if poolSize <= 0 {
		poolSize = 1
	}

	clients := make([]turncontrol.ServiceClient, 0, poolSize)
	conns := make([]*grpc.ClientConn, 0, poolSize)
	for i := 0; i < poolSize; i++ {
		conn, err := turncontrol.NewAuthenticatedClientConn(ctx, config.ControlGRPCAddress, config.ControlGRPCSecret)
		if err != nil {
			for _, openedConn := range conns {
				_ = openedConn.Close()
			}
			return nil, nil, err
		}
		clients = append(clients, turncontrol.NewServiceClient(conn))
		conns = append(conns, conn)
	}

	closeAll := func() error {
		var errs []error
		for _, conn := range conns {
			if conn == nil {
				continue
			}
			if err := conn.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	if len(clients) == 1 {
		return &grpcValidator{client: clients[0]}, closeAll, nil
	}

	return &pooledGRPCValidator{clients: clients}, closeAll, nil
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
	case "internal_error":
		return ErrValidationBackend
	default:
		return fmt.Errorf("turn control validation failed: %s", reason)
	}
}

func (s *Service) releaseAllocationSlot(ctx context.Context, username, operationID string) error {
	ctx, span := turnTracer.Start(ctx, "turn.allocation.release",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("turn.username", username),
			attribute.String("turn.release_operation_id", operationID),
		),
	)
	defer span.End()

	if err := s.validator.ReleaseTURNAllocation(ctx, username, operationID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		observability.RecordTURNRelayRelease(ctx, false)
		return err
	}

	observability.RecordTURNRelayRelease(ctx, true)
	return nil
}

func (s *Service) handleAllocationDeleted(srcAddr, dstAddr net.Addr, protocol, userID string) {
	allocation, ok := s.untrackActiveAllocation(srcAddr, dstAddr, protocol)
	if !ok {
		allocation = activeAllocation{
			Username: userID,
			SrcAddr:  cloneAddr(srcAddr),
			DstAddr:  cloneAddr(dstAddr),
			Protocol: strings.ToUpper(strings.TrimSpace(protocol)),
		}
	}

	go s.releaseDeletedAllocation(allocation, userID)
}

func (s *Service) releaseDeletedAllocation(allocation activeAllocation, fallbackUsername string) {
	releaseUserID := strings.TrimSpace(allocation.Username)
	if releaseUserID == "" {
		releaseUserID = strings.TrimSpace(fallbackUsername)
	}
	operationID := strings.TrimSpace(allocation.ReleaseOperationID)
	if releaseUserID == "" {
		log.Printf(
			"TURN allocation release skipped missing username protocol=%s peer_addr=%v relay_addr=%v",
			allocation.Protocol,
			allocation.SrcAddr,
			allocation.DstAddr,
		)
		return
	}
	if operationID == "" {
		log.Printf(
			"TURN allocation release skipped missing operation_id user=%q protocol=%s peer_addr=%v relay_addr=%v",
			releaseUserID,
			allocation.Protocol,
			allocation.SrcAddr,
			allocation.DstAddr,
		)
		return
	}

	queueCtx, queueCancel := context.WithTimeout(context.Background(), releaseAttemptTimeout)
	queueErr := s.validator.QueuePendingTURNRelease(queueCtx, releaseUserID, operationID)
	queueCancel()
	if queueErr != nil {
		log.Printf("TURN allocation release persistence failed user=%q operation_id=%s reason=%v", releaseUserID, operationID, queueErr)
		s.queueAllocationRelease(releaseUserID, operationID)
	}

	releaseCtx, cancel := context.WithTimeout(context.Background(), releaseAttemptTimeout)
	err := s.releaseAllocationSlot(releaseCtx, releaseUserID, operationID)
	cancel()
	if err == nil {
		if queueErr == nil {
			completeCtx, completeCancel := context.WithTimeout(context.Background(), releaseAttemptTimeout)
			completeErr := s.validator.CompletePendingTURNRelease(completeCtx, releaseUserID, operationID)
			completeCancel()
			if completeErr != nil {
				log.Printf("TURN allocation release completion deferred user=%q operation_id=%s reason=%v", releaseUserID, operationID, completeErr)
				s.signalReleaseWorker()
			}
		}
		s.completePendingRelease(releaseUserID, operationID)
		return
	}

	log.Printf("TURN allocation release retry scheduled user=%q operation_id=%s reason=%v", releaseUserID, operationID, err)
	s.signalReleaseWorker()
}

func (s *Service) logAuthAllowed(result ValidationResult, srcAddr net.Addr) {
	if result.SessionIP != "" {
		log.Printf("TURN auth allowed session=%s matched=%s peer_addr=%v session_ip=%s", result.SessionID, result.MatchedID, srcAddr, result.SessionIP)
		return
	}
	log.Printf("TURN auth allowed session=%s matched=%s peer_addr=%v", result.SessionID, result.MatchedID, srcAddr)
}

func formatAddr(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}

func (s *Service) queueAllocationRelease(username, operationID string) {
	username = strings.TrimSpace(username)
	operationID = strings.TrimSpace(operationID)
	if username == "" || operationID == "" {
		return
	}

	s.mu.Lock()
	if s.pendingReleases == nil {
		s.pendingReleases = make(map[string]map[string]struct{})
	}
	if s.pendingReleases[username] == nil {
		s.pendingReleases[username] = make(map[string]struct{})
	}
	s.pendingReleases[username][operationID] = struct{}{}
	s.mu.Unlock()

	s.signalReleaseWorker()
}

func (s *Service) signalReleaseWorker() {
	select {
	case s.releaseSignal <- struct{}{}:
	default:
	}
}

func (s *Service) flushPendingReleases(ctx context.Context, username string) error {
	return s.processPendingReleases(ctx, username)
}

func (s *Service) runReleaseWorker(ctx context.Context) {
	ticker := time.NewTicker(releaseRepairInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.releaseSignal:
			if err := s.processPendingReleases(ctx, ""); err != nil {
				log.Printf("TURN pending release processing failed: %v", err)
			}
		case <-ticker.C:
			if err := s.processPendingReleases(ctx, ""); err != nil {
				log.Printf("TURN pending release repair failed: %v", err)
			}
		}
	}
}

func pendingReleaseCompositeKey(username, operationID string) string {
	return username + "\x1f" + operationID
}

func (s *Service) pendingReleaseStates(ctx context.Context, username string) ([]pendingReleaseState, error) {
	states := make(map[string]pendingReleaseState)
	for _, release := range s.snapshotPendingReleases(username) {
		key := pendingReleaseCompositeKey(release.Username, release.OperationID)
		state := states[key]
		state.PendingRelease = release
		state.local = true
		states[key] = state
	}

	durable, err := s.validator.PendingTURNReleases(ctx, username)
	if err != nil && len(states) == 0 {
		return nil, err
	}
	for _, release := range durable {
		key := pendingReleaseCompositeKey(release.Username, release.OperationID)
		state := states[key]
		state.PendingRelease = release
		state.durable = true
		states[key] = state
	}

	releases := make([]pendingReleaseState, 0, len(states))
	for _, state := range states {
		releases = append(releases, state)
	}
	return releases, err
}

func (s *Service) processPendingReleases(ctx context.Context, username string) error {
	states, stateErr := s.pendingReleaseStates(ctx, username)
	if len(states) == 0 {
		return stateErr
	}

	var processErr error
	if stateErr != nil {
		processErr = stateErr
	}
	for _, state := range states {
		if state.local && !state.durable {
			queueCtx, queueCancel := context.WithTimeout(ctx, releaseAttemptTimeout)
			queueErr := s.validator.QueuePendingTURNRelease(queueCtx, state.Username, state.OperationID)
			queueCancel()
			if queueErr != nil {
				processErr = errors.Join(processErr, queueErr)
			} else {
				state.durable = true
			}
		}

		releaseCtx, cancel := context.WithTimeout(ctx, releaseAttemptTimeout)
		err := s.releaseAllocationSlot(releaseCtx, state.Username, state.OperationID)
		cancel()
		if err != nil {
			processErr = errors.Join(processErr, err)
			log.Printf("TURN allocation release failed user=%q operation_id=%s reason=%v", state.Username, state.OperationID, err)
			continue
		}

		if state.durable {
			completeCtx, completeCancel := context.WithTimeout(ctx, releaseAttemptTimeout)
			completeErr := s.validator.CompletePendingTURNRelease(completeCtx, state.Username, state.OperationID)
			completeCancel()
			if completeErr != nil {
				processErr = errors.Join(processErr, completeErr)
				log.Printf("TURN allocation release completion failed user=%q operation_id=%s reason=%v", state.Username, state.OperationID, completeErr)
				continue
			}
		}

		if state.local {
			s.completePendingRelease(state.Username, state.OperationID)
		}
	}
	return processErr
}

func (s *Service) snapshotPendingReleases(username string) []PendingRelease {
	s.mu.Lock()
	defer s.mu.Unlock()

	username = strings.TrimSpace(username)
	releases := make([]PendingRelease, 0)
	appendPending := func(userID string, operations map[string]struct{}) {
		for operationID := range operations {
			releases = append(releases, PendingRelease{
				Username:    userID,
				OperationID: operationID,
			})
		}
	}

	if username != "" {
		appendPending(username, s.pendingReleases[username])
		return releases
	}

	for userID, operations := range s.pendingReleases {
		appendPending(userID, operations)
	}
	return releases
}

func (s *Service) completePendingRelease(username, operationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	operations := s.pendingReleases[username]
	if operations == nil {
		return
	}
	delete(operations, operationID)
	if len(operations) == 0 {
		delete(s.pendingReleases, username)
	}
}

func (s *Service) nextReleaseOperationID() string {
	var randomBytes [12]byte
	if _, err := rand.Read(randomBytes[:]); err == nil {
		return "turn-release-" + hex.EncodeToString(randomBytes[:])
	}

	sequence := s.releaseSequence.Add(1)
	return "turn-release-fallback-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatUint(sequence, 10)
}
