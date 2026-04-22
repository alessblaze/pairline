package turnservice

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

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

const turnAllocationCounterTTL = 24 * time.Hour

const reserveAllocationSlotScriptSource = `
local current = redis.call("INCR", KEYS[1])
redis.call("PEXPIRE", KEYS[1], ARGV[2])
if current > tonumber(ARGV[1]) then
	local remaining = redis.call("DECR", KEYS[1])
	if remaining <= 0 then
		redis.call("DEL", KEYS[1])
	end
	return 0
end
return 1
`

const releaseAllocationSlotScriptSource = `
local remaining = redis.call("DECR", KEYS[1])
if remaining <= 0 then
	redis.call("DEL", KEYS[1])
	return 0
end
return remaining
`

var reserveAllocationSlotScript = redis.NewScript(reserveAllocationSlotScriptSource)
var releaseAllocationSlotScript = redis.NewScript(releaseAllocationSlotScriptSource)

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

	result, err := reserveAllocationSlotScript.Run(
		ctx,
		redisClient,
		[]string{turnAllocationKey(sessionID)},
		limit,
		turnAllocationCounterTTL.Milliseconds(),
	).Int()
	if err != nil {
		return false, ErrValidationBackend
	}

	return result == 1, nil
}

func ReleaseAllocationSlot(ctx context.Context, redisClient redis.UniversalClient, username string) error {
	sessionID, _, err := ParseUsername(username)
	if err != nil {
		return ErrInvalidSessionIdentity
	}

	if _, err := releaseAllocationSlotScript.Run(ctx, redisClient, []string{turnAllocationKey(sessionID)}).Int(); err != nil {
		return ErrValidationBackend
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
