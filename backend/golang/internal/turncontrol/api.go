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

type ReserveAllocationRequest struct {
	Username string `json:"username"`
	Limit    int32  `json:"limit"`
}

type CheckBannedSessionIPsRequest struct {
	SessionIPs []string `json:"session_ips"`
}

type ReserveAllocationResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

type CheckBannedSessionIPsResponse struct {
	BannedIPs []string `json:"banned_ips,omitempty"`
}

type PendingRelease struct {
	Username    string `json:"username"`
	OperationID string `json:"operation_id,omitempty"`
}

type ReleaseAllocationRequest struct {
	Username    string `json:"username"`
	OperationID string `json:"operation_id,omitempty"`
}

type QueuePendingReleaseRequest struct {
	Username    string `json:"username"`
	OperationID string `json:"operation_id,omitempty"`
}

type CompletePendingReleaseRequest struct {
	Username    string `json:"username"`
	OperationID string `json:"operation_id,omitempty"`
}

type PendingReleasesRequest struct {
	Username string `json:"username,omitempty"`
}

type ReleaseAllocationResponse struct {
	Released bool   `json:"released"`
	Reason   string `json:"reason,omitempty"`
}

type QueuePendingReleaseResponse struct {
	Queued bool   `json:"queued"`
	Reason string `json:"reason,omitempty"`
}

type CompletePendingReleaseResponse struct {
	Completed bool   `json:"completed"`
	Reason    string `json:"reason,omitempty"`
}

type PendingReleasesResponse struct {
	Releases []PendingRelease `json:"releases,omitempty"`
}

type ValidationResponse struct {
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Route     string `json:"route,omitempty"`
	MatchedID string `json:"matched_id,omitempty"`
	SessionIP string `json:"session_ip,omitempty"`
}

type ServiceServer interface {
	ValidateMatchedSession(context.Context, *ValidateMatchedSessionRequest) (*ValidationResponse, error)
	ValidateTurnUsername(context.Context, *ValidateTurnUsernameRequest) (*ValidationResponse, error)
	CheckBannedSessionIPs(context.Context, *CheckBannedSessionIPsRequest) (*CheckBannedSessionIPsResponse, error)
	ReserveAllocation(context.Context, *ReserveAllocationRequest) (*ReserveAllocationResponse, error)
	ReleaseAllocation(context.Context, *ReleaseAllocationRequest) (*ReleaseAllocationResponse, error)
	QueuePendingRelease(context.Context, *QueuePendingReleaseRequest) (*QueuePendingReleaseResponse, error)
	PendingReleases(context.Context, *PendingReleasesRequest) (*PendingReleasesResponse, error)
	CompletePendingRelease(context.Context, *CompletePendingReleaseRequest) (*CompletePendingReleaseResponse, error)
}

type ServiceClient interface {
	ValidateMatchedSession(context.Context, *ValidateMatchedSessionRequest, ...grpc.CallOption) (*ValidationResponse, error)
	ValidateTurnUsername(context.Context, *ValidateTurnUsernameRequest, ...grpc.CallOption) (*ValidationResponse, error)
	CheckBannedSessionIPs(context.Context, *CheckBannedSessionIPsRequest, ...grpc.CallOption) (*CheckBannedSessionIPsResponse, error)
	ReserveAllocation(context.Context, *ReserveAllocationRequest, ...grpc.CallOption) (*ReserveAllocationResponse, error)
	ReleaseAllocation(context.Context, *ReleaseAllocationRequest, ...grpc.CallOption) (*ReleaseAllocationResponse, error)
	QueuePendingRelease(context.Context, *QueuePendingReleaseRequest, ...grpc.CallOption) (*QueuePendingReleaseResponse, error)
	PendingReleases(context.Context, *PendingReleasesRequest, ...grpc.CallOption) (*PendingReleasesResponse, error)
	CompletePendingRelease(context.Context, *CompletePendingReleaseRequest, ...grpc.CallOption) (*CompletePendingReleaseResponse, error)
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

func (c *serviceClient) CheckBannedSessionIPs(ctx context.Context, in *CheckBannedSessionIPsRequest, opts ...grpc.CallOption) (*CheckBannedSessionIPsResponse, error) {
	out := new(CheckBannedSessionIPsResponse)
	err := c.cc.Invoke(ctx, "/pairline.turncontrol.Service/CheckBannedSessionIPs", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serviceClient) ReserveAllocation(ctx context.Context, in *ReserveAllocationRequest, opts ...grpc.CallOption) (*ReserveAllocationResponse, error) {
	out := new(ReserveAllocationResponse)
	err := c.cc.Invoke(ctx, "/pairline.turncontrol.Service/ReserveAllocation", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serviceClient) ReleaseAllocation(ctx context.Context, in *ReleaseAllocationRequest, opts ...grpc.CallOption) (*ReleaseAllocationResponse, error) {
	out := new(ReleaseAllocationResponse)
	err := c.cc.Invoke(ctx, "/pairline.turncontrol.Service/ReleaseAllocation", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serviceClient) QueuePendingRelease(ctx context.Context, in *QueuePendingReleaseRequest, opts ...grpc.CallOption) (*QueuePendingReleaseResponse, error) {
	out := new(QueuePendingReleaseResponse)
	err := c.cc.Invoke(ctx, "/pairline.turncontrol.Service/QueuePendingRelease", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serviceClient) PendingReleases(ctx context.Context, in *PendingReleasesRequest, opts ...grpc.CallOption) (*PendingReleasesResponse, error) {
	out := new(PendingReleasesResponse)
	err := c.cc.Invoke(ctx, "/pairline.turncontrol.Service/PendingReleases", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serviceClient) CompletePendingRelease(ctx context.Context, in *CompletePendingReleaseRequest, opts ...grpc.CallOption) (*CompletePendingReleaseResponse, error) {
	out := new(CompletePendingReleaseResponse)
	err := c.cc.Invoke(ctx, "/pairline.turncontrol.Service/CompletePendingRelease", in, out, opts...)
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
			{
				MethodName: "CheckBannedSessionIPs",
				Handler:    checkBannedSessionIPsHandler,
			},
			{
				MethodName: "ReserveAllocation",
				Handler:    reserveAllocationHandler,
			},
			{
				MethodName: "ReleaseAllocation",
				Handler:    releaseAllocationHandler,
			},
			{
				MethodName: "QueuePendingRelease",
				Handler:    queuePendingReleaseHandler,
			},
			{
				MethodName: "PendingReleases",
				Handler:    pendingReleasesHandler,
			},
			{
				MethodName: "CompletePendingRelease",
				Handler:    completePendingReleaseHandler,
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

func checkBannedSessionIPsHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CheckBannedSessionIPsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServiceServer).CheckBannedSessionIPs(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/pairline.turncontrol.Service/CheckBannedSessionIPs",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServiceServer).CheckBannedSessionIPs(ctx, req.(*CheckBannedSessionIPsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func reserveAllocationHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ReserveAllocationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServiceServer).ReserveAllocation(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/pairline.turncontrol.Service/ReserveAllocation",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServiceServer).ReserveAllocation(ctx, req.(*ReserveAllocationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func releaseAllocationHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ReleaseAllocationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServiceServer).ReleaseAllocation(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/pairline.turncontrol.Service/ReleaseAllocation",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServiceServer).ReleaseAllocation(ctx, req.(*ReleaseAllocationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func queuePendingReleaseHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(QueuePendingReleaseRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServiceServer).QueuePendingRelease(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/pairline.turncontrol.Service/QueuePendingRelease",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServiceServer).QueuePendingRelease(ctx, req.(*QueuePendingReleaseRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func pendingReleasesHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PendingReleasesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServiceServer).PendingReleases(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/pairline.turncontrol.Service/PendingReleases",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServiceServer).PendingReleases(ctx, req.(*PendingReleasesRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func completePendingReleaseHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CompletePendingReleaseRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServiceServer).CompletePendingRelease(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/pairline.turncontrol.Service/CompletePendingRelease",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServiceServer).CompletePendingRelease(ctx, req.(*CompletePendingReleaseRequest))
	}
	return interceptor(ctx, in, info, handler)
}
