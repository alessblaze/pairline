package turnservice

import (
	"context"
	"testing"

	"github.com/anish/omegle/backend/golang/internal/turncontrol"
	"google.golang.org/grpc"
)

type fakeTurnControlClient struct {
	response        *turncontrol.ValidationResponse
	reserveResponse *turncontrol.ReserveAllocationResponse
	releaseResponse *turncontrol.ReleaseAllocationResponse
	err             error
}

func (c *fakeTurnControlClient) ValidateMatchedSession(context.Context, *turncontrol.ValidateMatchedSessionRequest, ...grpc.CallOption) (*turncontrol.ValidationResponse, error) {
	return &turncontrol.ValidationResponse{Allowed: true}, nil
}

func (c *fakeTurnControlClient) ValidateTurnUsername(context.Context, *turncontrol.ValidateTurnUsernameRequest, ...grpc.CallOption) (*turncontrol.ValidationResponse, error) {
	return c.response, c.err
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
