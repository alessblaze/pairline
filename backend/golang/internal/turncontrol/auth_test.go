package turncontrol

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type testServiceServer struct {
	validateTurnUsername func(context.Context, *ValidateTurnUsernameRequest) (*ValidationResponse, error)
}

func (s *testServiceServer) ValidateMatchedSession(context.Context, *ValidateMatchedSessionRequest) (*ValidationResponse, error) {
	return &ValidationResponse{Allowed: true}, nil
}

func (s *testServiceServer) ValidateTurnUsername(ctx context.Context, req *ValidateTurnUsernameRequest) (*ValidationResponse, error) {
	return s.validateTurnUsername(ctx, req)
}

func (s *testServiceServer) ReserveAllocation(context.Context, *ReserveAllocationRequest) (*ReserveAllocationResponse, error) {
	return &ReserveAllocationResponse{Allowed: true}, nil
}

func (s *testServiceServer) ReleaseAllocation(context.Context, *ReleaseAllocationRequest) (*ReleaseAllocationResponse, error) {
	return &ReleaseAllocationResponse{Released: true}, nil
}

func startTestServer(t *testing.T, sharedSecret string, srv ServiceServer) (*bufconn.Listener, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer(
		grpc.ForceServerCodec(JSONCodec),
		grpc.UnaryInterceptor(AuthUnaryServerInterceptor(sharedSecret)),
	)
	RegisterServiceServer(grpcServer, srv)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = grpcServer.Serve(listener)
	}()

	cleanup := func() {
		grpcServer.GracefulStop()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("gRPC server did not stop in time")
		}
	}

	return listener, cleanup
}

func TestAuthenticatedClientConnRoundTrip(t *testing.T) {
	listener, cleanup := startTestServer(t, "shared-secret", &testServiceServer{
		validateTurnUsername: func(_ context.Context, req *ValidateTurnUsernameRequest) (*ValidationResponse, error) {
			if req.Username != "session|digest" {
				t.Fatalf("username = %q, want %q", req.Username, "session|digest")
			}
			return &ValidationResponse{
				Allowed:   true,
				SessionID: "session",
				Route:     "video|7",
				MatchedID: "peer",
			}, nil
		},
	})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := newAuthenticatedClientConn(
		ctx,
		"bufnet",
		"shared-secret",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatalf("newAuthenticatedClientConn() error = %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Fatalf("conn.Close() error = %v", err)
		}
	}()

	client := NewServiceClient(conn)
	resp, err := client.ValidateTurnUsername(ctx, &ValidateTurnUsernameRequest{Username: "session|digest"})
	if err != nil {
		t.Fatalf("ValidateTurnUsername() error = %v", err)
	}
	if !resp.Allowed {
		t.Fatalf("Allowed = false, want true")
	}
	if resp.Route != "video|7" {
		t.Fatalf("Route = %q, want %q", resp.Route, "video|7")
	}
}

func TestAuthenticatedClientConnRejectsWrongSecret(t *testing.T) {
	listener, cleanup := startTestServer(t, "expected-secret", &testServiceServer{
		validateTurnUsername: func(_ context.Context, _ *ValidateTurnUsernameRequest) (*ValidationResponse, error) {
			return &ValidationResponse{Allowed: true}, nil
		},
	})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := newAuthenticatedClientConn(
		ctx,
		"bufnet",
		"wrong-secret",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatalf("newAuthenticatedClientConn() error = %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Fatalf("conn.Close() error = %v", err)
		}
	}()

	client := NewServiceClient(conn)
	_, err = client.ValidateTurnUsername(ctx, &ValidateTurnUsernameRequest{Username: "session|digest"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("ValidateTurnUsername() code = %v, want %v (err=%v)", status.Code(err), codes.Unauthenticated, err)
	}
}
