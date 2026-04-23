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
	if err := turnservice.ReleaseAllocationSlot(ctx, s.redisClient, req.Username); err != nil {
		return &turncontrol.ReleaseAllocationResponse{
			Released: false,
			Reason:   turnservice.ValidationErrorReason(err),
		}, nil
	}
	return &turncontrol.ReleaseAllocationResponse{Released: true}, nil
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
