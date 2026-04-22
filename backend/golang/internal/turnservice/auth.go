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
		return ValidationResult{}, ErrSessionNotFound
	}

	expectedToken, err := redisClient.Get(ctx, appredis.SessionTokenKey(sessionID, route)).Result()
	if err != nil || expectedToken == "" {
		return ValidationResult{}, ErrSessionInactive
	}

	hash := sha256.Sum256([]byte(providedToken))
	providedHashHex := hex.EncodeToString(hash[:])
	if subtle.ConstantTimeCompare([]byte(expectedToken), []byte(providedHashHex)) != 1 {
		return ValidationResult{}, ErrInvalidSessionIdentity
	}

	sessionExists, err := redisClient.Exists(ctx, appredis.SessionDataKey(sessionID, route)).Result()
	if err != nil || sessionExists == 0 {
		return ValidationResult{}, ErrSessionInactive
	}

	if banned, err := redisClient.Exists(ctx, appredis.BanSessionKey(sessionID)).Result(); err == nil && banned > 0 {
		return ValidationResult{}, ErrSessionBanned
	}

	if sessionIP, err := redisClient.Get(ctx, appredis.SessionIPKey(sessionID, route)).Result(); err == nil && strings.TrimSpace(sessionIP) != "" {
		if banned, banErr := redisClient.Exists(ctx, appredis.BanIPKey(sessionIP)).Result(); banErr == nil && banned > 0 {
			return ValidationResult{}, ErrSessionBanned
		}
	}

	matchedID, err := redisClient.Get(ctx, appredis.MatchKey(sessionID, route)).Result()
	if err != nil || strings.TrimSpace(matchedID) == "" {
		return ValidationResult{}, ErrSessionUnmatched
	}

	return ValidationResult{
		SessionID: sessionID,
		Route:     route,
		MatchedID: matchedID,
	}, nil
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
		return ValidationResult{}, ErrSessionNotFound
	}

	expectedToken, err := redisClient.Get(ctx, appredis.SessionTokenKey(sessionID, route)).Result()
	if err != nil || expectedToken == "" {
		return ValidationResult{}, ErrSessionInactive
	}

	if subtle.ConstantTimeCompare([]byte(expectedToken), []byte(tokenDigest)) != 1 {
		return ValidationResult{}, ErrInvalidSessionIdentity
	}

	sessionExists, err := redisClient.Exists(ctx, appredis.SessionDataKey(sessionID, route)).Result()
	if err != nil || sessionExists == 0 {
		return ValidationResult{}, ErrSessionInactive
	}

	if banned, err := redisClient.Exists(ctx, appredis.BanSessionKey(sessionID)).Result(); err == nil && banned > 0 {
		return ValidationResult{}, ErrSessionBanned
	}

	if sessionIP, err := redisClient.Get(ctx, appredis.SessionIPKey(sessionID, route)).Result(); err == nil && strings.TrimSpace(sessionIP) != "" {
		if banned, banErr := redisClient.Exists(ctx, appredis.BanIPKey(sessionIP)).Result(); banErr == nil && banned > 0 {
			return ValidationResult{}, ErrSessionBanned
		}
	}

	matchedID, err := redisClient.Get(ctx, appredis.MatchKey(sessionID, route)).Result()
	if err != nil || strings.TrimSpace(matchedID) == "" {
		return ValidationResult{}, ErrSessionUnmatched
	}

	return ValidationResult{
		SessionID: sessionID,
		Route:     route,
		MatchedID: matchedID,
	}, nil
}
