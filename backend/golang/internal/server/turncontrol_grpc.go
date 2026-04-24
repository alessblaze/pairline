package server

import (
	"context"
	"fmt"

	"github.com/anish/omegle/backend/golang/internal/turncontrol"
	"github.com/anish/omegle/backend/golang/internal/turnservice"
	"github.com/redis/go-redis/v9"
)

type turnControlValidationServer struct {
	redisClient redis.UniversalClient
}

func newTurnControlValidationServer(redisClient redis.UniversalClient) turncontrol.ServiceServer {
	return &turnControlValidationServer{redisClient: redisClient}
}

func (s *turnControlValidationServer) ValidateMatchedSession(ctx context.Context, req *turncontrol.ValidateMatchedSessionRequest) (*turncontrol.ValidationResponse, error) {
	result, err := turnservice.ValidateMatchedSession(ctx, s.redisClient, req.SessionID, req.SessionToken)
	if err != nil {
		return validationErrorResponse(err), nil
	}
	return validationSuccessResponse(result), nil
}

func (s *turnControlValidationServer) ValidateTurnUsername(ctx context.Context, req *turncontrol.ValidateTurnUsernameRequest) (*turncontrol.ValidationResponse, error) {
	result, err := turnservice.ValidateTURNUsername(ctx, s.redisClient, req.Username)
	if err != nil {
		return validationErrorResponse(err), nil
	}
	return validationSuccessResponse(result), nil
}

func (s *turnControlValidationServer) CheckBannedSessionIPs(ctx context.Context, req *turncontrol.CheckBannedSessionIPsRequest) (*turncontrol.CheckBannedSessionIPsResponse, error) {
	bannedIPs, err := turnservice.CheckBannedSessionIPs(ctx, s.redisClient, req.SessionIPs)
	if err != nil {
		return nil, err
	}
	return &turncontrol.CheckBannedSessionIPsResponse{BannedIPs: bannedIPs}, nil
}

func (s *turnControlValidationServer) ReserveAllocation(ctx context.Context, req *turncontrol.ReserveAllocationRequest) (*turncontrol.ReserveAllocationResponse, error) {
	allowed, err := turnservice.ReserveAllocationSlot(ctx, s.redisClient, req.Username, int(req.Limit))
	if err != nil {
		return &turncontrol.ReserveAllocationResponse{
			Allowed: false,
			Reason:  turnservice.ValidationErrorReason(err),
		}, nil
	}
	return &turncontrol.ReserveAllocationResponse{Allowed: allowed}, nil
}

func (s *turnControlValidationServer) ReleaseAllocation(ctx context.Context, req *turncontrol.ReleaseAllocationRequest) (*turncontrol.ReleaseAllocationResponse, error) {
	if err := turnservice.ReleaseAllocationSlot(ctx, s.redisClient, req.Username, req.OperationID); err != nil {
		return &turncontrol.ReleaseAllocationResponse{
			Released: false,
			Reason:   turnservice.ValidationErrorReason(err),
		}, nil
	}
	return &turncontrol.ReleaseAllocationResponse{Released: true}, nil
}

func (s *turnControlValidationServer) QueuePendingRelease(ctx context.Context, req *turncontrol.QueuePendingReleaseRequest) (*turncontrol.QueuePendingReleaseResponse, error) {
	if err := turnservice.QueuePendingRelease(ctx, s.redisClient, req.Username, req.OperationID); err != nil {
		return &turncontrol.QueuePendingReleaseResponse{
			Queued: false,
			Reason: turnservice.ValidationErrorReason(err),
		}, nil
	}
	return &turncontrol.QueuePendingReleaseResponse{Queued: true}, nil
}

func (s *turnControlValidationServer) PendingReleases(ctx context.Context, req *turncontrol.PendingReleasesRequest) (*turncontrol.PendingReleasesResponse, error) {
	releases, err := turnservice.PendingReleases(ctx, s.redisClient, req.Username)
	if err != nil {
		return nil, err
	}

	response := &turncontrol.PendingReleasesResponse{
		Releases: make([]turncontrol.PendingRelease, 0, len(releases)),
	}
	for _, release := range releases {
		response.Releases = append(response.Releases, turncontrol.PendingRelease{
			Username:    release.Username,
			OperationID: release.OperationID,
		})
	}
	return response, nil
}

func (s *turnControlValidationServer) CompletePendingRelease(ctx context.Context, req *turncontrol.CompletePendingReleaseRequest) (*turncontrol.CompletePendingReleaseResponse, error) {
	if err := turnservice.CompletePendingRelease(ctx, s.redisClient, req.Username, req.OperationID); err != nil {
		return &turncontrol.CompletePendingReleaseResponse{
			Completed: false,
			Reason:    turnservice.ValidationErrorReason(err),
		}, nil
	}
	return &turncontrol.CompletePendingReleaseResponse{Completed: true}, nil
}

func validationSuccessResponse(result turnservice.ValidationResult) *turncontrol.ValidationResponse {
	return &turncontrol.ValidationResponse{
		Allowed:   true,
		SessionID: result.SessionID,
		Route:     fmt.Sprintf("%s|%d", result.Route.Mode, result.Route.Shard),
		MatchedID: result.MatchedID,
		SessionIP: result.SessionIP,
	}
}

func validationErrorResponse(err error) *turncontrol.ValidationResponse {
	return &turncontrol.ValidationResponse{
		Allowed:   false,
		Reason:    turnservice.ValidationErrorReason(err),
		SessionIP: turnservice.ValidationErrorSessionIP(err),
	}
}
