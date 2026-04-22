package turnservice

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/redis/go-redis/v9"
)

var (
	ErrInvalidSessionIdentity = errors.New("invalid session identity")
	ErrSessionNotFound        = errors.New("session not found")
	ErrSessionInactive        = errors.New("session inactive")
	ErrSessionUnmatched       = errors.New("session not matched")
	ErrSessionBanned          = errors.New("session banned")
	ErrValidationBackend      = errors.New("session validation backend failure")
)

type ValidationResult struct {
	SessionID string
	Route     appredis.SessionRoute
	MatchedID string
}

func ValidateMatchedSession(ctx context.Context, redisClient redis.UniversalClient, sessionID, providedToken string) (ValidationResult, error) {
	if sessionID == "" || providedToken == "" {
		return ValidationResult{}, ErrInvalidSessionIdentity
	}

	route, err := appredis.ResolveSessionRoute(ctx, redisClient, sessionID)
	if err != nil {
		return ValidationResult{}, validationRouteError(err)
	}

	hash := sha256.Sum256([]byte(providedToken))
	providedHashHex := hex.EncodeToString(hash[:])
	return validateMatchedRouteSession(ctx, redisClient, route, sessionID, func(expectedToken string) bool {
		return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(providedHashHex)) == 1
	})
}

func ValidateTURNUsername(ctx context.Context, redisClient redis.UniversalClient, username string) (ValidationResult, error) {
	sessionID, tokenDigest, err := ParseUsername(username)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("%w: %v", ErrInvalidSessionIdentity, err)
	}

	if sessionID == "" || tokenDigest == "" {
		return ValidationResult{}, ErrInvalidSessionIdentity
	}

	route, err := appredis.ResolveSessionRoute(ctx, redisClient, sessionID)
	if err != nil {
		return ValidationResult{}, validationRouteError(err)
	}

	return validateMatchedRouteSession(ctx, redisClient, route, sessionID, func(expectedToken string) bool {
		return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(tokenDigest)) == 1
	})
}

func validateMatchedRouteSession(
	ctx context.Context,
	redisClient redis.UniversalClient,
	route appredis.SessionRoute,
	sessionID string,
	tokenMatches func(expectedToken string) bool,
) (ValidationResult, error) {
	expectedToken, err := redisClient.Get(ctx, appredis.SessionTokenKey(sessionID, route)).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return ValidationResult{}, ErrSessionInactive
	case err != nil:
		return ValidationResult{}, ErrValidationBackend
	case expectedToken == "":
		return ValidationResult{}, ErrSessionInactive
	}

	if !tokenMatches(expectedToken) {
		return ValidationResult{}, ErrInvalidSessionIdentity
	}

	sessionExists, err := redisClient.Exists(ctx, appredis.SessionDataKey(sessionID, route)).Result()
	if err != nil {
		return ValidationResult{}, ErrValidationBackend
	}
	if sessionExists == 0 {
		return ValidationResult{}, ErrSessionInactive
	}

	banned, err := redisClient.Exists(ctx, appredis.BanSessionKey(sessionID)).Result()
	if err != nil {
		return ValidationResult{}, ErrValidationBackend
	}
	if banned > 0 {
		return ValidationResult{}, ErrSessionBanned
	}

	sessionIP, err := redisClient.Get(ctx, appredis.SessionIPKey(sessionID, route)).Result()
	switch {
	case errors.Is(err, redis.Nil):
	case err != nil:
		return ValidationResult{}, ErrValidationBackend
	case strings.TrimSpace(sessionIP) != "":
		ipBanned, banErr := redisClient.Exists(ctx, appredis.BanIPKey(sessionIP)).Result()
		if banErr != nil {
			return ValidationResult{}, ErrValidationBackend
		}
		if ipBanned > 0 {
			return ValidationResult{}, ErrSessionBanned
		}
	}

	matchedID, err := redisClient.Get(ctx, appredis.MatchKey(sessionID, route)).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return ValidationResult{}, ErrSessionUnmatched
	case err != nil:
		return ValidationResult{}, ErrValidationBackend
	case strings.TrimSpace(matchedID) == "":
		return ValidationResult{}, ErrSessionUnmatched
	}

	peerRoute, err := appredis.ResolveSessionRoute(ctx, redisClient, matchedID)
	switch {
	case errors.Is(err, appredis.ErrSessionRouteNotFound):
		return ValidationResult{}, ErrSessionUnmatched
	case err != nil:
		return ValidationResult{}, ErrValidationBackend
	}

	peerExists, err := redisClient.Exists(ctx, appredis.SessionDataKey(matchedID, peerRoute)).Result()
	if err != nil {
		return ValidationResult{}, ErrValidationBackend
	}
	if peerExists == 0 {
		return ValidationResult{}, ErrSessionUnmatched
	}

	peerMatchedID, err := redisClient.Get(ctx, appredis.MatchKey(matchedID, peerRoute)).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return ValidationResult{}, ErrSessionUnmatched
	case err != nil:
		return ValidationResult{}, ErrValidationBackend
	case strings.TrimSpace(peerMatchedID) != sessionID:
		return ValidationResult{}, ErrSessionUnmatched
	}

	return ValidationResult{
		SessionID: sessionID,
		Route:     route,
		MatchedID: matchedID,
	}, nil
}

func validationRouteError(err error) error {
	switch {
	case errors.Is(err, appredis.ErrSessionRouteNotFound):
		return ErrSessionNotFound
	case errors.Is(err, appredis.ErrInvalidSessionRoute):
		return ErrValidationBackend
	default:
		return ErrValidationBackend
	}
}

func turnAllocationKey(sessionID string) string {
	return "turn:allocations:" + sessionID
}

func ReserveAllocationSlot(ctx context.Context, redisClient redis.UniversalClient, username string, limit int) (bool, error) {
	if limit <= 0 {
		return true, nil
	}

	sessionID, _, err := ParseUsername(username)
	if err != nil {
		return false, ErrInvalidSessionIdentity
	}

	allocations, err := redisClient.Incr(ctx, turnAllocationKey(sessionID)).Result()
	if err != nil {
		return false, ErrValidationBackend
	}
	if allocations > int64(limit) {
		if _, decErr := redisClient.Decr(ctx, turnAllocationKey(sessionID)).Result(); decErr != nil {
			return false, ErrValidationBackend
		}
		return false, nil
	}

	return true, nil
}

func ReleaseAllocationSlot(ctx context.Context, redisClient redis.UniversalClient, username string) error {
	sessionID, _, err := ParseUsername(username)
	if err != nil {
		return ErrInvalidSessionIdentity
	}

	allocations, err := redisClient.Decr(ctx, turnAllocationKey(sessionID)).Result()
	if err != nil {
		return ErrValidationBackend
	}
	if allocations <= 0 {
		if delErr := redisClient.Del(ctx, turnAllocationKey(sessionID)).Err(); delErr != nil {
			return ErrValidationBackend
		}
	}

	return nil
}

func ValidationErrorReason(err error) string {
	switch {
	case errors.Is(err, ErrInvalidSessionIdentity):
		return "invalid_session_identity"
	case errors.Is(err, ErrSessionNotFound):
		return "session_not_found"
	case errors.Is(err, ErrSessionInactive):
		return "session_inactive"
	case errors.Is(err, ErrSessionUnmatched):
		return "session_unmatched"
	case errors.Is(err, ErrSessionBanned):
		return "session_banned"
	case errors.Is(err, ErrValidationBackend):
		return "internal_error"
	default:
		return "internal_error"
	}
}
