package turncontrol

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const authMetadataKey = "x-pairline-turn-control-auth"

func NewAuthenticatedClientConn(ctx context.Context, addr, sharedSecret string) (*grpc.ClientConn, error) {
	return newAuthenticatedClientConn(ctx, addr, sharedSecret)
}

func newAuthenticatedClientConn(ctx context.Context, addr, sharedSecret string, extraOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
	if addr == "" {
		return nil, fmt.Errorf("turn control grpc address is required")
	}
	if sharedSecret == "" {
		return nil, fmt.Errorf("turn control grpc shared secret is required")
	}

	_ = ctx

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(JSONCodec)),
		grpc.WithUnaryInterceptor(clientAuthInterceptor(sharedSecret)),
	}
	opts = append(opts, extraOpts...)

	return grpc.NewClient(grpcTarget(addr), opts...)
}

func grpcTarget(addr string) string {
	if strings.Contains(addr, "://") {
		return addr
	}
	return "passthrough:///" + addr
}

func AuthUnaryServerInterceptor(sharedSecret string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if sharedSecret == "" {
			return nil, status.Error(codes.Internal, "turn control grpc auth is not configured")
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing auth metadata")
		}

		values := md.Get(authMetadataKey)
		if len(values) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing auth metadata")
		}

		if subtle.ConstantTimeCompare([]byte(values[0]), []byte(sharedSecret)) != 1 {
			return nil, status.Error(codes.Unauthenticated, "invalid auth metadata")
		}

		return handler(ctx, req)
	}
}

func clientAuthInterceptor(sharedSecret string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, authMetadataKey, sharedSecret)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
