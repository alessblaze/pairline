package turnservice

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/anish/omegle/backend/golang/internal/turncontrol"
	pionturn "github.com/pion/turn/v5"
	"google.golang.org/grpc"
)

type fakeTurnControlClient struct {
	response          *turncontrol.ValidationResponse
	bannedIPsResponse *turncontrol.CheckBannedSessionIPsResponse
	reserveResponse   *turncontrol.ReserveAllocationResponse
	releaseResponse   *turncontrol.ReleaseAllocationResponse
	queueResponse     *turncontrol.QueuePendingReleaseResponse
	pendingResponse   *turncontrol.PendingReleasesResponse
	completeResponse  *turncontrol.CompletePendingReleaseResponse
	err               error
}

func (c *fakeTurnControlClient) ValidateMatchedSession(context.Context, *turncontrol.ValidateMatchedSessionRequest, ...grpc.CallOption) (*turncontrol.ValidationResponse, error) {
	return &turncontrol.ValidationResponse{Allowed: true}, nil
}

func (c *fakeTurnControlClient) ValidateTurnUsername(context.Context, *turncontrol.ValidateTurnUsernameRequest, ...grpc.CallOption) (*turncontrol.ValidationResponse, error) {
	return c.response, c.err
}

func (c *fakeTurnControlClient) CheckBannedSessionIPs(context.Context, *turncontrol.CheckBannedSessionIPsRequest, ...grpc.CallOption) (*turncontrol.CheckBannedSessionIPsResponse, error) {
	return c.bannedIPsResponse, c.err
}

func (c *fakeTurnControlClient) ReserveAllocation(context.Context, *turncontrol.ReserveAllocationRequest, ...grpc.CallOption) (*turncontrol.ReserveAllocationResponse, error) {
	return c.reserveResponse, c.err
}

func (c *fakeTurnControlClient) ReleaseAllocation(context.Context, *turncontrol.ReleaseAllocationRequest, ...grpc.CallOption) (*turncontrol.ReleaseAllocationResponse, error) {
	return c.releaseResponse, c.err
}

func (c *fakeTurnControlClient) QueuePendingRelease(context.Context, *turncontrol.QueuePendingReleaseRequest, ...grpc.CallOption) (*turncontrol.QueuePendingReleaseResponse, error) {
	return c.queueResponse, c.err
}

func (c *fakeTurnControlClient) PendingReleases(context.Context, *turncontrol.PendingReleasesRequest, ...grpc.CallOption) (*turncontrol.PendingReleasesResponse, error) {
	return c.pendingResponse, c.err
}

func (c *fakeTurnControlClient) CompletePendingRelease(context.Context, *turncontrol.CompletePendingReleaseRequest, ...grpc.CallOption) (*turncontrol.CompletePendingReleaseResponse, error) {
	return c.completeResponse, c.err
}

func TestGRPCValidatorValidateTURNUsername(t *testing.T) {
	validator := &grpcValidator{client: &fakeTurnControlClient{
		response: &turncontrol.ValidationResponse{
			Allowed:   true,
			SessionID: "session-1",
			Route:     "video|3",
			MatchedID: "session-2",
			SessionIP: "203.0.113.24",
		},
	}}

	result, err := validator.ValidateTURNUsername(context.Background(), "session-1|digest")
	if err != nil {
		t.Fatalf("ValidateTURNUsername() error = %v", err)
	}
	if result.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want %q", result.SessionID, "session-1")
	}
	if result.Route.Mode != "video" || result.Route.Shard != 3 {
		t.Fatalf("Route = %#v, want mode=%q shard=%d", result.Route, "video", 3)
	}
	if result.MatchedID != "session-2" {
		t.Fatalf("MatchedID = %q, want %q", result.MatchedID, "session-2")
	}
	if result.SessionIP != "203.0.113.24" {
		t.Fatalf("SessionIP = %q, want %q", result.SessionIP, "203.0.113.24")
	}
}

func TestGRPCValidatorValidateTURNUsernameIncludesSessionIPOnDeniedResponse(t *testing.T) {
	validator := &grpcValidator{client: &fakeTurnControlClient{
		response: &turncontrol.ValidationResponse{
			Allowed:   false,
			Reason:    "session_banned",
			SessionIP: "203.0.113.24",
		},
	}}

	_, err := validator.ValidateTURNUsername(context.Background(), "session-1|digest")
	if err == nil {
		t.Fatal("ValidateTURNUsername() error = nil, want banned error")
	}
	if !errors.Is(err, ErrSessionBanned) {
		t.Fatalf("ValidateTURNUsername() error = %v, want %v", err, ErrSessionBanned)
	}
	if got := ValidationErrorSessionIP(err); got != "203.0.113.24" {
		t.Fatalf("ValidationErrorSessionIP() = %q, want %q", got, "203.0.113.24")
	}
}

func TestGRPCValidatorValidateTURNUsernamePreservesSessionIPOnUnknownDeniedReason(t *testing.T) {
	validator := &grpcValidator{client: &fakeTurnControlClient{
		response: &turncontrol.ValidationResponse{
			Allowed:   false,
			Reason:    "upstream_policy_denied",
			SessionIP: "203.0.113.24",
		},
	}}

	_, err := validator.ValidateTURNUsername(context.Background(), "session-1|digest")
	if err == nil {
		t.Fatal("ValidateTURNUsername() error = nil, want denial error")
	}
	if got := ValidationErrorSessionIP(err); got != "203.0.113.24" {
		t.Fatalf("ValidationErrorSessionIP() = %q, want %q", got, "203.0.113.24")
	}
}

func TestGRPCValidatorCheckBannedSessionIPs(t *testing.T) {
	validator := &grpcValidator{client: &fakeTurnControlClient{
		bannedIPsResponse: &turncontrol.CheckBannedSessionIPsResponse{
			BannedIPs: []string{"203.0.113.24"},
		},
	}}

	got, err := validator.CheckBannedSessionIPs(context.Background(), []string{"203.0.113.24", "198.51.100.8"})
	if err != nil {
		t.Fatalf("CheckBannedSessionIPs() error = %v", err)
	}
	if len(got) != 1 || got[0] != "203.0.113.24" {
		t.Fatalf("CheckBannedSessionIPs() = %#v, want only banned IP", got)
	}
}

func TestGRPCValidatorReserveAndReleaseTURNAllocation(t *testing.T) {
	validator := &grpcValidator{client: &fakeTurnControlClient{
		reserveResponse: &turncontrol.ReserveAllocationResponse{Allowed: true},
		releaseResponse: &turncontrol.ReleaseAllocationResponse{Released: true},
	}}

	allowed, err := validator.ReserveTURNAllocation(context.Background(), "session-1|digest", 2)
	if err != nil {
		t.Fatalf("ReserveTURNAllocation() error = %v", err)
	}
	if !allowed {
		t.Fatal("ReserveTURNAllocation() = false, want true")
	}

	if err := validator.ReleaseTURNAllocation(context.Background(), "session-1|digest", "release-1"); err != nil {
		t.Fatalf("ReleaseTURNAllocation() error = %v", err)
	}
}

func TestPooledGRPCValidatorCyclesClients(t *testing.T) {
	clientOne := &fakeTurnControlClient{
		response: &turncontrol.ValidationResponse{
			Allowed:   true,
			SessionID: "session-1",
			Route:     "video|1",
			MatchedID: "peer-1",
		},
	}
	clientTwo := &fakeTurnControlClient{
		response: &turncontrol.ValidationResponse{
			Allowed:   true,
			SessionID: "session-2",
			Route:     "video|2",
			MatchedID: "peer-2",
		},
	}

	validator := &pooledGRPCValidator{
		clients: []turncontrol.ServiceClient{clientOne, clientTwo},
	}

	first, err := validator.ValidateTURNUsername(context.Background(), "session-1|digest")
	if err != nil {
		t.Fatalf("first ValidateTURNUsername() error = %v", err)
	}
	second, err := validator.ValidateTURNUsername(context.Background(), "session-2|digest")
	if err != nil {
		t.Fatalf("second ValidateTURNUsername() error = %v", err)
	}

	if first.Route.Shard != 1 {
		t.Fatalf("first shard = %d, want 1", first.Route.Shard)
	}
	if second.Route.Shard != 2 {
		t.Fatalf("second shard = %d, want 2", second.Route.Shard)
	}
}

type fakeValidator struct {
	bannedIPs      []string
	validateResult ValidationResult
	validateErr    error
	validateCalls  int
	releaseCalls   int
	queueCalls     int
	completeCalls  int
	releasedOps    []string
	queuedOps      []string
	completedOps   []string
	releaseErr     error
	reserveFn      func(context.Context, string, int) (bool, error)
	releaseFn      func(context.Context, string, string) error
	queueFn        func(context.Context, string, string) error
	pendingFn      func(context.Context, string) ([]PendingRelease, error)
	completeFn     func(context.Context, string, string) error
}

func (v *fakeValidator) ValidateTURNUsername(context.Context, string) (ValidationResult, error) {
	v.validateCalls++
	return v.validateResult, v.validateErr
}

func (v *fakeValidator) CheckBannedSessionIPs(context.Context, []string) ([]string, error) {
	return append([]string(nil), v.bannedIPs...), nil
}

func (v *fakeValidator) ReserveTURNAllocation(ctx context.Context, username string, limit int) (bool, error) {
	if v.reserveFn != nil {
		return v.reserveFn(ctx, username, limit)
	}
	return true, nil
}

func (v *fakeValidator) ReleaseTURNAllocation(ctx context.Context, username, operationID string) error {
	v.releaseCalls++
	v.releasedOps = append(v.releasedOps, username+"|"+operationID)
	if v.releaseFn != nil {
		return v.releaseFn(ctx, username, operationID)
	}
	return v.releaseErr
}

func (v *fakeValidator) QueuePendingTURNRelease(ctx context.Context, username, operationID string) error {
	v.queueCalls++
	v.queuedOps = append(v.queuedOps, username+"|"+operationID)
	if v.queueFn != nil {
		return v.queueFn(ctx, username, operationID)
	}
	return nil
}

func (v *fakeValidator) PendingTURNReleases(ctx context.Context, username string) ([]PendingRelease, error) {
	if v.pendingFn != nil {
		return v.pendingFn(ctx, username)
	}
	return nil, nil
}

func (v *fakeValidator) CompletePendingTURNRelease(ctx context.Context, username, operationID string) error {
	v.completeCalls++
	v.completedOps = append(v.completedOps, username+"|"+operationID)
	if v.completeFn != nil {
		return v.completeFn(ctx, username, operationID)
	}
	return nil
}

func TestServiceRevokeBannedAllocations(t *testing.T) {
	revoked := make([]activeAllocation, 0, 1)
	svc := &Service{
		validator: &fakeValidator{bannedIPs: []string{"203.0.113.24"}},
		activeAllocations: map[string]activeAllocation{
			activeAllocationKey(&net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}, &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}, "UDP"): {
				Username:  "ignored",
				SessionIP: "203.0.113.24",
				SrcAddr:   &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111},
				DstAddr:   &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478},
				Protocol:  "UDP",
			},
			activeAllocationKey(&net.UDPAddr{IP: net.ParseIP("10.0.0.3"), Port: 2222}, &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}, "UDP"): {
				Username:  "ignored-2",
				SessionIP: "198.51.100.8",
				SrcAddr:   &net.UDPAddr{IP: net.ParseIP("10.0.0.3"), Port: 2222},
				DstAddr:   &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478},
				Protocol:  "UDP",
			},
		},
		revokeAllocation: func(allocation activeAllocation) error {
			revoked = append(revoked, allocation)
			return nil
		},
	}
	svc.allocationKeysBySession = map[string]map[string]struct{}{
		"203.0.113.24": {
			activeAllocationKey(&net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}, &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}, "UDP"): {},
		},
		"198.51.100.8": {
			activeAllocationKey(&net.UDPAddr{IP: net.ParseIP("10.0.0.3"), Port: 2222}, &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}, "UDP"): {},
		},
	}

	if err := svc.revokeBannedAllocations(context.Background()); err != nil {
		t.Fatalf("revokeBannedAllocations() error = %v", err)
	}
	if len(revoked) != 1 {
		t.Fatalf("len(revoked) = %d, want 1", len(revoked))
	}
	if revoked[0].SessionIP != "203.0.113.24" {
		t.Fatalf("revoked session IP = %q, want banned IP", revoked[0].SessionIP)
	}
}

func TestServiceUntrackActiveAllocationCleansSessionIPCache(t *testing.T) {
	svc := &Service{
		activeAllocations: map[string]activeAllocation{
			activeAllocationKey(&net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}, &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}, "UDP"): {
				Username:  "user-1",
				SessionIP: "203.0.113.24",
				SrcAddr:   &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111},
				DstAddr:   &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478},
				Protocol:  "UDP",
			},
		},
		sessionIPByUserID: map[string]rememberedSessionIP{
			"user-1": {IP: "203.0.113.24", SeenAt: time.Now()},
		},
		allocationKeysByUserID: map[string]map[string]struct{}{
			"user-1": {
				activeAllocationKey(&net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}, &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}, "UDP"): {},
			},
		},
		allocationCountByUserID: map[string]int{"user-1": 1},
		allocationKeysBySession: map[string]map[string]struct{}{
			"203.0.113.24": {
				activeAllocationKey(&net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}, &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}, "UDP"): {},
			},
		},
	}

	_, _ = svc.untrackActiveAllocation(
		&net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111},
		&net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478},
		"UDP",
	)

	if _, ok := svc.sessionIPByUserID["user-1"]; ok {
		t.Fatal("sessionIPByUserID still contains user after final allocation removal")
	}
}

func TestRememberSessionIPUpdatesTrackedAllocations(t *testing.T) {
	allocationKey := activeAllocationKey(&net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}, &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}, "UDP")
	svc := &Service{
		activeAllocations: map[string]activeAllocation{
			allocationKey: {
				Username:           "user-1",
				SessionIP:          "",
				SrcAddr:            &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111},
				DstAddr:            &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478},
				Protocol:           "UDP",
				ReleaseOperationID: "release-1",
			},
		},
		sessionIPByUserID: make(map[string]rememberedSessionIP),
		allocationKeysByUserID: map[string]map[string]struct{}{
			"user-1": {allocationKey: {}},
		},
		allocationCountByUserID: map[string]int{"user-1": 1},
		allocationKeysBySession: make(map[string]map[string]struct{}),
	}

	svc.rememberSessionIP("user-1", "203.0.113.24")

	for _, allocation := range svc.activeAllocations {
		if allocation.SessionIP != "203.0.113.24" {
			t.Fatalf("allocation.SessionIP = %q, want %q", allocation.SessionIP, "203.0.113.24")
		}
	}
	if _, ok := svc.allocationKeysBySession["203.0.113.24"][allocationKey]; !ok {
		t.Fatal("allocationKeysBySession did not track updated session IP")
	}
}

func TestCleanupRememberedSessionIPsDropsStaleEntriesWithoutAllocations(t *testing.T) {
	svc := &Service{
		activeAllocations: map[string]activeAllocation{},
		sessionIPByUserID: map[string]rememberedSessionIP{
			"user-1": {
				IP:     "203.0.113.24",
				SeenAt: time.Now().Add(-rememberedSessionIPTTL - time.Second),
			},
		},
		allocationCountByUserID: map[string]int{},
	}

	svc.mu.Lock()
	svc.cleanupRememberedSessionIPsLocked(time.Now())
	svc.mu.Unlock()

	if _, ok := svc.sessionIPByUserID["user-1"]; ok {
		t.Fatal("stale remembered session IP was not cleaned up")
	}
}

func TestTurnAllocationProtocol(t *testing.T) {
	if got := turnAllocationProtocol("UDP"); got != 0 {
		t.Fatalf("turnAllocationProtocol(UDP) = %d, want 0", got)
	}
	if got := turnAllocationProtocol("TCP"); got != 1 {
		t.Fatalf("turnAllocationProtocol(TCP) = %d, want 1", got)
	}
}

func TestRevokeTurnAllocationRejectsMissingAddresses(t *testing.T) {
	err := revokeTurnAllocation(&pionturn.Server{}, activeAllocation{})
	if err == nil {
		t.Fatal("revokeTurnAllocation() error = nil, want missing address error")
	}
}

func TestServiceFlushPendingReleases(t *testing.T) {
	validator := &fakeValidator{}
	svc := &Service{
		validator:       validator,
		pendingReleases: map[string]map[string]localPendingRelease{"user-1": {"release-1": {queuedAt: time.Now()}}},
		releaseSignal:   make(chan struct{}, 1),
	}

	if err := svc.flushPendingReleases(context.Background(), "user-1"); err != nil {
		t.Fatalf("flushPendingReleases() error = %v", err)
	}
	if validator.releaseCalls != 1 {
		t.Fatalf("releaseCalls = %d, want 1", validator.releaseCalls)
	}
	if validator.queueCalls != 1 {
		t.Fatalf("queueCalls = %d, want 1", validator.queueCalls)
	}
	if validator.completeCalls != 1 {
		t.Fatalf("completeCalls = %d, want 1", validator.completeCalls)
	}
	if len(svc.pendingReleases) != 0 {
		t.Fatalf("pendingReleases = %#v, want empty", svc.pendingReleases)
	}
}

func TestServiceReleaseDeletedAllocationDoesImmediateRelease(t *testing.T) {
	validator := &fakeValidator{}
	svc := &Service{
		validator:       validator,
		pendingReleases: make(map[string]map[string]localPendingRelease),
		releaseSignal:   make(chan struct{}, 1),
	}

	svc.releaseDeletedAllocation(activeAllocation{
		Username:           "user-1",
		ReleaseOperationID: "release-1",
	}, "")

	if validator.releaseCalls != 1 {
		t.Fatalf("releaseCalls = %d, want 1", validator.releaseCalls)
	}
	if validator.queueCalls != 1 {
		t.Fatalf("queueCalls = %d, want 1", validator.queueCalls)
	}
	if validator.completeCalls != 1 {
		t.Fatalf("completeCalls = %d, want 1", validator.completeCalls)
	}
	if len(svc.pendingReleases) != 0 {
		t.Fatalf("pendingReleases = %#v, want empty", svc.pendingReleases)
	}
}

func TestServiceReleaseDeletedAllocationQueuesRetryOnFailure(t *testing.T) {
	validator := &fakeValidator{
		releaseErr: errors.New("release failed"),
		queueFn: func(context.Context, string, string) error {
			return errors.New("queue failed")
		},
	}
	svc := &Service{
		validator:       validator,
		pendingReleases: make(map[string]map[string]localPendingRelease),
		releaseSignal:   make(chan struct{}, 1),
	}

	svc.releaseDeletedAllocation(activeAllocation{
		ReleaseOperationID: "release-1",
	}, "fallback-user")

	if validator.releaseCalls != 1 {
		t.Fatalf("releaseCalls = %d, want 1", validator.releaseCalls)
	}
	if validator.queueCalls != 1 {
		t.Fatalf("queueCalls = %d, want 1", validator.queueCalls)
	}
	if _, ok := svc.pendingReleases["fallback-user"]["release-1"]; !ok {
		t.Fatalf("pendingReleases = %#v, want queued fallback release", svc.pendingReleases)
	}
}

func TestServiceReleaseDeletedAllocationLogsMissingOperationID(t *testing.T) {
	var logBuffer bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logBuffer)
	defer log.SetOutput(previousWriter)

	validator := &fakeValidator{}
	svc := &Service{
		validator:       validator,
		pendingReleases: make(map[string]map[string]localPendingRelease),
		releaseSignal:   make(chan struct{}, 1),
	}

	svc.releaseDeletedAllocation(activeAllocation{
		Username: "user-1",
		Protocol: "UDP",
		SrcAddr:  &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111},
		DstAddr:  &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478},
	}, "")

	if validator.releaseCalls != 0 {
		t.Fatalf("releaseCalls = %d, want 0", validator.releaseCalls)
	}
	if !strings.Contains(logBuffer.String(), "missing operation_id") {
		t.Fatalf("log output = %q, want missing operation_id warning", logBuffer.String())
	}
}

func TestServiceHandleAllocationDeletedReturnsBeforeReleaseCompletes(t *testing.T) {
	releaseStarted := make(chan struct{}, 1)
	releaseBlock := make(chan struct{})
	validator := &fakeValidator{
		releaseFn: func(context.Context, string, string) error {
			releaseStarted <- struct{}{}
			<-releaseBlock
			return nil
		},
	}
	srcAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}
	dstAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}
	svc := &Service{
		validator: validator,
		activeAllocations: map[string]activeAllocation{
			activeAllocationKey(srcAddr, dstAddr, "UDP"): {
				Username:           "user-1",
				ReleaseOperationID: "release-1",
				SrcAddr:            srcAddr,
				DstAddr:            dstAddr,
				Protocol:           "UDP",
			},
		},
		pendingReleases: make(map[string]map[string]localPendingRelease),
		releaseSignal:   make(chan struct{}, 1),
	}

	returned := make(chan struct{})
	go func() {
		svc.handleAllocationDeleted(srcAddr, dstAddr, "UDP", "user-1")
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("handleAllocationDeleted blocked on release work")
	}

	select {
	case <-releaseStarted:
	case <-time.After(time.Second):
		t.Fatal("release goroutine did not start")
	}

	close(releaseBlock)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if validator.completeCalls == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("completeCalls = %d, want 1", validator.completeCalls)
}

func TestServiceUntrackActiveAllocationReturnsReleaseFallback(t *testing.T) {
	srcAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}
	dstAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}
	allocationKey := activeAllocationKey(srcAddr, dstAddr, "UDP")

	svc := &Service{
		activeAllocations:       make(map[string]activeAllocation),
		allocationReleaseLookup: map[string]activeAllocation{allocationKey: {Username: "user-1", ReleaseOperationID: "release-1"}},
	}

	allocation, ok := svc.untrackActiveAllocation(srcAddr, dstAddr, "UDP")
	if !ok {
		t.Fatal("untrackActiveAllocation() = false, want true from release fallback")
	}
	if allocation.Username != "user-1" || allocation.ReleaseOperationID != "release-1" {
		t.Fatalf("untrackActiveAllocation() = %#v, want fallback allocation metadata", allocation)
	}
	if _, ok := svc.allocationReleaseLookup[allocationKey]; ok {
		t.Fatalf("allocationReleaseLookup[%q] still present after fallback untrack", allocationKey)
	}
}

func TestTrackActiveAllocationPreservesSessionIPOnReplacement(t *testing.T) {
	srcAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}
	dstAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}
	allocationKey := activeAllocationKey(srcAddr, dstAddr, "UDP")

	svc := &Service{
		activeAllocations: map[string]activeAllocation{
			allocationKey: {
				Username:           "user-1",
				SessionIP:          "203.0.113.24",
				SrcAddr:            srcAddr,
				DstAddr:            dstAddr,
				Protocol:           "UDP",
				ReleaseOperationID: "release-old",
			},
		},
		allocationReleaseLookup: map[string]activeAllocation{
			allocationKey: {
				Username:           "user-1",
				SessionIP:          "203.0.113.24",
				SrcAddr:            srcAddr,
				DstAddr:            dstAddr,
				Protocol:           "UDP",
				ReleaseOperationID: "release-old",
			},
		},
		allocationKeysByUserID: map[string]map[string]struct{}{
			"user-1": {allocationKey: {}},
		},
		allocationKeysBySession: map[string]map[string]struct{}{
			"203.0.113.24": {allocationKey: {}},
		},
		allocationCountByUserID: map[string]int{"user-1": 1},
		sessionIPByUserID:       make(map[string]rememberedSessionIP),
	}

	svc.trackActiveAllocation(srcAddr, dstAddr, "UDP", "user-1")

	allocation := svc.activeAllocations[allocationKey]
	if allocation.SessionIP != "203.0.113.24" {
		t.Fatalf("replacement allocation SessionIP = %q, want %q", allocation.SessionIP, "203.0.113.24")
	}
	if got := svc.sessionIPByUserID["user-1"].IP; got != "203.0.113.24" {
		t.Fatalf("sessionIPByUserID[user-1] = %q, want %q", got, "203.0.113.24")
	}
	if _, ok := svc.allocationKeysBySession["203.0.113.24"][allocationKey]; !ok {
		t.Fatalf("allocationKeysBySession = %#v, want replacement key to remain indexed", svc.allocationKeysBySession)
	}
	if allocation.ReleaseOperationID == "" || allocation.ReleaseOperationID == "release-old" {
		t.Fatalf("replacement ReleaseOperationID = %q, want fresh non-empty ID", allocation.ReleaseOperationID)
	}
}

func TestServiceSnapshotPendingReleasesIncludesActiveSessions(t *testing.T) {
	svc := &Service{
		pendingReleases: map[string]map[string]localPendingRelease{
			"user-idle":   {"release-1": {queuedAt: time.Now()}},
			"user-active": {"release-2": {queuedAt: time.Now()}},
		},
	}

	pending := svc.snapshotPendingReleases("")
	if len(pending) != 2 {
		t.Fatalf("len(snapshotPendingReleases) = %d, want 2", len(pending))
	}
}

func TestProcessPendingReleasesMergesDurableAndLocalEntries(t *testing.T) {
	validator := &fakeValidator{
		pendingFn: func(context.Context, string) ([]PendingRelease, error) {
			return []PendingRelease{
				{Username: "user-durable", OperationID: "release-1"},
			}, nil
		},
	}
	svc := &Service{
		validator: validator,
		pendingReleases: map[string]map[string]localPendingRelease{
			"user-local": {"release-2": {queuedAt: time.Now()}},
		},
		releaseSignal: make(chan struct{}, 1),
	}

	if err := svc.processPendingReleases(context.Background(), ""); err != nil {
		t.Fatalf("processPendingReleases() error = %v", err)
	}
	if validator.releaseCalls != 2 {
		t.Fatalf("releaseCalls = %d, want 2", validator.releaseCalls)
	}
	if validator.queueCalls != 1 {
		t.Fatalf("queueCalls = %d, want 1", validator.queueCalls)
	}
	if validator.completeCalls != 2 {
		t.Fatalf("completeCalls = %d, want 2", validator.completeCalls)
	}
	if len(svc.pendingReleases) != 0 {
		t.Fatalf("pendingReleases = %#v, want empty", svc.pendingReleases)
	}
}

func TestProcessPendingReleasesDropsLocalEntryAfterDurableQueueSucceeds(t *testing.T) {
	validator := &fakeValidator{
		releaseFn: func(context.Context, string, string) error {
			return errors.New("release still failing")
		},
	}
	svc := &Service{
		validator: validator,
		pendingReleases: map[string]map[string]localPendingRelease{
			"user-1": {"release-1": {queuedAt: time.Now()}},
		},
		releaseSignal: make(chan struct{}, 1),
	}

	if err := svc.processPendingReleases(context.Background(), ""); err == nil {
		t.Fatal("processPendingReleases() error = nil, want release error")
	}
	if validator.queueCalls != 1 {
		t.Fatalf("queueCalls = %d, want 1", validator.queueCalls)
	}
	if validator.releaseCalls != 1 {
		t.Fatalf("releaseCalls = %d, want 1", validator.releaseCalls)
	}
	if _, ok := svc.pendingReleases["user-1"]; ok {
		t.Fatalf("pendingReleases = %#v, want local entry dropped after durable queue", svc.pendingReleases)
	}
}

func TestCleanupExpiredPendingReleasesDropsOldEntries(t *testing.T) {
	svc := &Service{
		pendingReleases: map[string]map[string]localPendingRelease{
			"user-old": {
				"release-old": {queuedAt: time.Now().Add(-pendingReleaseMaxAge - time.Minute)},
			},
			"user-fresh": {
				"release-fresh": {queuedAt: time.Now()},
			},
		},
	}

	svc.mu.Lock()
	svc.cleanupExpiredPendingReleasesLocked(time.Now())
	svc.mu.Unlock()

	if _, ok := svc.pendingReleases["user-old"]; ok {
		t.Fatalf("pendingReleases = %#v, want expired entry removed", svc.pendingReleases)
	}
	if _, ok := svc.pendingReleases["user-fresh"]["release-fresh"]; !ok {
		t.Fatalf("pendingReleases = %#v, want fresh entry retained", svc.pendingReleases)
	}
}

func TestReconcileTrackedAllocationsRemovesMissingServerEntries(t *testing.T) {
	srcAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}
	dstAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}
	allocationKey := activeAllocationKey(srcAddr, dstAddr, "UDP")
	releaseStarted := make(chan struct{}, 1)
	releaseBlock := make(chan struct{})
	validator := &fakeValidator{
		releaseFn: func(context.Context, string, string) error {
			releaseStarted <- struct{}{}
			<-releaseBlock
			return nil
		},
	}
	svc := &Service{
		validator: validator,
		activeAllocations: map[string]activeAllocation{
			allocationKey: {
				Username:           "user-1",
				SessionIP:          "203.0.113.24",
				SrcAddr:            srcAddr,
				DstAddr:            dstAddr,
				Protocol:           "UDP",
				ReleaseOperationID: "release-1",
			},
		},
		allocationReleaseLookup: map[string]activeAllocation{
			allocationKey: {
				Username:           "user-1",
				SessionIP:          "203.0.113.24",
				SrcAddr:            srcAddr,
				DstAddr:            dstAddr,
				Protocol:           "UDP",
				ReleaseOperationID: "release-1",
			},
		},
		allocationKeysByUserID: map[string]map[string]struct{}{
			"user-1": {allocationKey: {}},
		},
		allocationKeysBySession: map[string]map[string]struct{}{
			"203.0.113.24": {allocationKey: {}},
		},
		allocationCountByUserID: map[string]int{"user-1": 1},
		sessionIPByUserID: map[string]rememberedSessionIP{
			"user-1": {IP: "203.0.113.24", SeenAt: time.Now()},
		},
		pendingReleases: make(map[string]map[string]localPendingRelease),
		releaseSignal:   make(chan struct{}, 1),
		hasAllocation: func(activeAllocation) (bool, error) {
			return false, nil
		},
	}

	if err := svc.reconcileTrackedAllocations(); err != nil {
		t.Fatalf("reconcileTrackedAllocations() error = %v", err)
	}
	if len(svc.activeAllocations) != 0 || len(svc.allocationReleaseLookup) != 0 {
		t.Fatalf("allocation tracking still present after reconcile: active=%#v lookup=%#v", svc.activeAllocations, svc.allocationReleaseLookup)
	}

	select {
	case <-releaseStarted:
	case <-time.After(time.Second):
		t.Fatal("releaseDeletedAllocation was not invoked for reconciled allocation")
	}
	close(releaseBlock)
}

func TestReconcileTrackedAllocationsRecoversFromProbePanic(t *testing.T) {
	srcAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}
	dstAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478}
	allocationKey := activeAllocationKey(srcAddr, dstAddr, "UDP")
	svc := &Service{
		activeAllocations: map[string]activeAllocation{
			allocationKey: {
				Username:           "user-1",
				SrcAddr:            srcAddr,
				DstAddr:            dstAddr,
				Protocol:           "UDP",
				ReleaseOperationID: "release-1",
			},
		},
		allocationReleaseLookup: map[string]activeAllocation{
			allocationKey: {
				Username:           "user-1",
				SrcAddr:            srcAddr,
				DstAddr:            dstAddr,
				Protocol:           "UDP",
				ReleaseOperationID: "release-1",
			},
		},
		allocationKeysByUserID: map[string]map[string]struct{}{
			"user-1": {allocationKey: {}},
		},
		allocationCountByUserID: map[string]int{"user-1": 1},
		hasAllocation: func(activeAllocation) (bool, error) {
			panic("boom")
		},
	}

	err := svc.reconcileTrackedAllocations()
	if err == nil {
		t.Fatal("reconcileTrackedAllocations() error = nil, want recovered panic error")
	}
	if _, ok := svc.activeAllocations[allocationKey]; !ok {
		t.Fatalf("activeAllocations = %#v, want allocation retained after probe panic", svc.activeAllocations)
	}
}

func TestQuotaHandlerUsesFreshReserveContextAfterSlowFlush(t *testing.T) {
	validator := &fakeValidator{
		releaseFn: func(ctx context.Context, _, _ string) error {
			select {
			case <-time.After(700 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		reserveFn: func(ctx context.Context, username string, limit int) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			return true, nil
		},
	}
	svc := &Service{
		config:    Config{AllocationQuota: 1},
		validator: validator,
		pendingReleases: map[string]map[string]localPendingRelease{
			"user-1": {
				"release-1": {queuedAt: time.Now()},
				"release-2": {queuedAt: time.Now()},
				"release-3": {queuedAt: time.Now()},
			},
		},
		releaseSignal: make(chan struct{}, 1),
	}

	if allowed := svc.quotaHandler("user-1", "pairline", &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111}); !allowed {
		t.Fatal("quotaHandler() = false, want true")
	}
	if validator.releaseCalls != 3 {
		t.Fatalf("releaseCalls = %d, want 3", validator.releaseCalls)
	}
}

func TestQuotaHandlerHandlesNilSourceAddr(t *testing.T) {
	validator := &fakeValidator{
		reserveFn: func(context.Context, string, int) (bool, error) {
			return true, nil
		},
	}
	svc := &Service{
		config:          Config{AllocationQuota: 1},
		validator:       validator,
		pendingReleases: make(map[string]map[string]localPendingRelease),
		releaseSignal:   make(chan struct{}, 1),
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("quotaHandler panicked with nil source address: %v", recovered)
		}
	}()

	if allowed := svc.quotaHandler("user-1", "pairline", nil); !allowed {
		t.Fatal("quotaHandler() = false, want true")
	}
}
