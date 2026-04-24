package turnservice

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/anish/omegle/backend/golang/internal/turncontrol"
	"google.golang.org/grpc"
)

type fakeTurnControlClient struct {
	response          *turncontrol.ValidationResponse
	bannedIPsResponse *turncontrol.CheckBannedSessionIPsResponse
	reserveResponse   *turncontrol.ReserveAllocationResponse
	releaseResponse   *turncontrol.ReleaseAllocationResponse
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

	if err := validator.ReleaseTURNAllocation(context.Background(), "session-1|digest"); err != nil {
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
}

func (v *fakeValidator) ValidateTURNUsername(context.Context, string) (ValidationResult, error) {
	v.validateCalls++
	return v.validateResult, v.validateErr
}

func (v *fakeValidator) CheckBannedSessionIPs(context.Context, []string) ([]string, error) {
	return append([]string(nil), v.bannedIPs...), nil
}

func (v *fakeValidator) ReserveTURNAllocation(context.Context, string, int) (bool, error) {
	return true, nil
}

func (v *fakeValidator) ReleaseTURNAllocation(context.Context, string) error {
	v.releaseCalls++
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

	svc.untrackActiveAllocation(
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
				Username:  "user-1",
				SessionIP: "",
				SrcAddr:   &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1111},
				DstAddr:   &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 3478},
				Protocol:  "UDP",
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
