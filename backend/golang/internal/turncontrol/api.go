package turncontrol

import (
	"context"

	"google.golang.org/grpc"
)

type ValidateMatchedSessionRequest struct {
	SessionID    string `json:"session_id"`
	SessionToken string `json:"session_token"`
}

type ValidateTurnUsernameRequest struct {
	Username string `json:"username"`
}

type ValidationResponse struct {
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Route     string `json:"route,omitempty"`
	MatchedID string `json:"matched_id,omitempty"`
}

type ServiceServer interface {
	ValidateMatchedSession(context.Context, *ValidateMatchedSessionRequest) (*ValidationResponse, error)
	ValidateTurnUsername(context.Context, *ValidateTurnUsernameRequest) (*ValidationResponse, error)
}

type ServiceClient interface {
	ValidateMatchedSession(context.Context, *ValidateMatchedSessionRequest, ...grpc.CallOption) (*ValidationResponse, error)
	ValidateTurnUsername(context.Context, *ValidateTurnUsernameRequest, ...grpc.CallOption) (*ValidationResponse, error)
}

type serviceClient struct {
	cc grpc.ClientConnInterface
}

func NewServiceClient(cc grpc.ClientConnInterface) ServiceClient {
	return &serviceClient{cc: cc}
}

func (c *serviceClient) ValidateMatchedSession(ctx context.Context, in *ValidateMatchedSessionRequest, opts ...grpc.CallOption) (*ValidationResponse, error) {
	out := new(ValidationResponse)
	err := c.cc.Invoke(ctx, "/pairline.turncontrol.Service/ValidateMatchedSession", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serviceClient) ValidateTurnUsername(ctx context.Context, in *ValidateTurnUsernameRequest, opts ...grpc.CallOption) (*ValidationResponse, error) {
	out := new(ValidationResponse)
	err := c.cc.Invoke(ctx, "/pairline.turncontrol.Service/ValidateTurnUsername", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func RegisterServiceServer(s grpc.ServiceRegistrar, srv ServiceServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "pairline.turncontrol.Service",
		HandlerType: (*ServiceServer)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "ValidateMatchedSession",
				Handler:    validateMatchedSessionHandler,
			},
			{
				MethodName: "ValidateTurnUsername",
				Handler:    validateTurnUsernameHandler,
			},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "turncontrol",
	}, srv)
}

func validateMatchedSessionHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ValidateMatchedSessionRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServiceServer).ValidateMatchedSession(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/pairline.turncontrol.Service/ValidateMatchedSession",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServiceServer).ValidateMatchedSession(ctx, req.(*ValidateMatchedSessionRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func validateTurnUsernameHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ValidateTurnUsernameRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServiceServer).ValidateTurnUsername(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/pairline.turncontrol.Service/ValidateTurnUsername",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServiceServer).ValidateTurnUsername(ctx, req.(*ValidateTurnUsernameRequest))
	}
	return interceptor(ctx, in, info, handler)
}
