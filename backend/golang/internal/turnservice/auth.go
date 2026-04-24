package turnservice

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
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
const turnPendingReleaseIndexKey = "turn:pending_releases"

const reserveAllocationSlotScriptSource = `
local keyType = redis.call("TYPE", KEYS[1]).ok
if keyType == "string" then
	local current = tonumber(redis.call("GET", KEYS[1]) or "0")
	redis.call("DEL", KEYS[1])
	if current > 0 then
		redis.call("HSET", KEYS[1], "count", current)
	end
elseif keyType ~= "hash" and keyType ~= "none" then
	return redis.error_reply("WRONGTYPE unexpected allocation counter type")
end

local current = redis.call("HINCRBY", KEYS[1], "count", 1)
redis.call("PEXPIRE", KEYS[1], ARGV[2])
if current > tonumber(ARGV[1]) then
	local remaining = redis.call("HINCRBY", KEYS[1], "count", -1)
	if remaining <= 0 then
		redis.call("DEL", KEYS[1])
	end
	return 0
end
return 1
`

const releaseAllocationSlotScriptSource = `
local keyType = redis.call("TYPE", KEYS[1]).ok
local operationField = "release:" .. ARGV[1]
if keyType == "none" then
	redis.call("HSET", KEYS[1], "count", 0)
	redis.call("HSET", KEYS[1], operationField, 1)
	redis.call("PEXPIRE", KEYS[1], ARGV[2])
	return 0
end

if keyType == "string" then
	local current = tonumber(redis.call("GET", KEYS[1]) or "0")
	redis.call("DEL", KEYS[1])
	if current > 0 then
		redis.call("HSET", KEYS[1], "count", current)
	end
elseif keyType ~= "hash" then
	return redis.error_reply("WRONGTYPE unexpected allocation counter type")
end

if redis.call("HEXISTS", KEYS[1], operationField) == 1 then
	return tonumber(redis.call("HGET", KEYS[1], "count") or "0")
end

local remaining = redis.call("HINCRBY", KEYS[1], "count", -1)
redis.call("HSET", KEYS[1], operationField, 1)
if remaining <= 0 then
	redis.call("HSET", KEYS[1], "count", 0)
	redis.call("PEXPIRE", KEYS[1], ARGV[2])
	return 0
end
redis.call("PEXPIRE", KEYS[1], ARGV[2])
return remaining
`

const sessionValidationSnapshotScriptSource = `
local expectedToken = redis.call("GET", KEYS[1]) or ""
local sessionExists = redis.call("EXISTS", KEYS[2])
local sessionIP = redis.call("GET", KEYS[3]) or ""
local matchedID = redis.call("GET", KEYS[4]) or ""
return {expectedToken, sessionExists, sessionIP, matchedID}
`

const peerValidationSnapshotScriptSource = `
local peerExists = redis.call("EXISTS", KEYS[1])
local peerMatchedID = redis.call("GET", KEYS[2]) or ""
return {peerExists, peerMatchedID}
`

const checkBannedSessionIPsBatchScriptSource = `
local banned = {}
for i, key in ipairs(KEYS) do
	if redis.call("EXISTS", key) > 0 then
		table.insert(banned, ARGV[i])
	end
end
return banned
`

var reserveAllocationSlotScript = redis.NewScript(reserveAllocationSlotScriptSource)
var releaseAllocationSlotScript = redis.NewScript(releaseAllocationSlotScriptSource)
var sessionValidationSnapshotScript = redis.NewScript(sessionValidationSnapshotScriptSource)
var peerValidationSnapshotScript = redis.NewScript(peerValidationSnapshotScriptSource)
var checkBannedSessionIPsBatchScript = redis.NewScript(checkBannedSessionIPsBatchScriptSource)
var turnScripts = []*redis.Script{
	reserveAllocationSlotScript,
	releaseAllocationSlotScript,
	sessionValidationSnapshotScript,
	peerValidationSnapshotScript,
	checkBannedSessionIPsBatchScript,
}

type ValidationResult struct {
	SessionID string
	Route     appredis.SessionRoute
	MatchedID string
	SessionIP string
}

type ValidationError struct {
	cause     error
	sessionIP string
}

func (e *ValidationError) Error() string {
	return e.cause.Error()
}

func (e *ValidationError) Unwrap() error {
	return e.cause
}

func (e *ValidationError) SessionIP() string {
	return e.sessionIP
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
	var (
		sessionValues    interface{}
		sessionBannedCnt int64
	)
	err := runConcurrent(
		func() error {
			var runErr error
			sessionValues, runErr = sessionValidationSnapshotScript.Run(
				ctx,
				redisClient,
				[]string{
					appredis.SessionTokenKey(sessionID, route),
					appredis.SessionDataKey(sessionID, route),
					appredis.SessionIPKey(sessionID, route),
					appredis.MatchKey(sessionID, route),
				},
			).Result()
			return runErr
		},
		func() error {
			var runErr error
			sessionBannedCnt, runErr = redisClient.Exists(ctx, appredis.BanSessionKey(sessionID)).Result()
			return runErr
		},
	)
	if err != nil {
		return ValidationResult{}, ErrValidationBackend
	}

	sessionSnapshot, ok := redisValueSlice(sessionValues)
	if !ok {
		return ValidationResult{}, ErrValidationBackend
	}

	expectedToken, _ := redisValueAsString(sessionSnapshot, 0)
	if expectedToken == "" {
		return ValidationResult{}, ErrSessionInactive
	}

	if !tokenMatches(expectedToken) {
		return ValidationResult{}, ErrInvalidSessionIdentity
	}

	if !redisValueAsBool(sessionSnapshot, 1) {
		return ValidationResult{}, ErrSessionInactive
	}

	sessionIP, _ := redisValueAsString(sessionSnapshot, 2)
	sessionIP = strings.TrimSpace(sessionIP)

	if sessionBannedCnt > 0 {
		return ValidationResult{}, validationErrorWithSessionIP(ErrSessionBanned, sessionIP)
	}

	matchedID, _ := redisValueAsString(sessionSnapshot, 3)
	if strings.TrimSpace(matchedID) == "" {
		return ValidationResult{}, ErrSessionUnmatched
	}

	var (
		peerRoute       appredis.SessionRoute
		ipBanned        int64
		errPeerNotFound = errors.New("peer route not found")
	)
	tasks := []func() error{
		func() error {
			resolvedRoute, routeErr := appredis.ResolveSessionRoute(ctx, redisClient, matchedID)
			switch {
			case errors.Is(routeErr, appredis.ErrSessionRouteNotFound):
				return errPeerNotFound
			case routeErr != nil:
				return routeErr
			default:
				peerRoute = resolvedRoute
				return nil
			}
		},
	}
	if sessionIP != "" {
		tasks = append(tasks, func() error {
			var banErr error
			ipBanned, banErr = redisClient.Exists(ctx, appredis.BanIPKey(sessionIP)).Result()
			return banErr
		})
	}
	err = runConcurrent(tasks...)
	if ipBanned > 0 {
		return ValidationResult{}, validationErrorWithSessionIP(ErrSessionBanned, sessionIP)
	}
	switch {
	case errors.Is(err, errPeerNotFound):
		return ValidationResult{}, ErrSessionUnmatched
	case err != nil:
		return ValidationResult{}, ErrValidationBackend
	}

	peerValues, err := peerValidationSnapshotScript.Run(
		ctx,
		redisClient,
		[]string{
			appredis.SessionDataKey(matchedID, peerRoute),
			appredis.MatchKey(matchedID, peerRoute),
		},
	).Result()
	if err != nil {
		return ValidationResult{}, ErrValidationBackend
	}

	peerSnapshot, ok := redisValueSlice(peerValues)
	if !ok {
		return ValidationResult{}, ErrValidationBackend
	}
	if !redisValueAsBool(peerSnapshot, 0) {
		return ValidationResult{}, ErrSessionUnmatched
	}

	peerMatchedID, _ := redisValueAsString(peerSnapshot, 1)
	if strings.TrimSpace(peerMatchedID) != sessionID {
		return ValidationResult{}, ErrSessionUnmatched
	}

	return ValidationResult{
		SessionID: sessionID,
		Route:     route,
		MatchedID: matchedID,
		SessionIP: sessionIP,
	}, nil
}

func redisValueSlice(value interface{}) ([]interface{}, bool) {
	values, ok := value.([]interface{})
	if !ok {
		return nil, false
	}

	return values, true
}

func redisValueAsString(values []interface{}, index int) (string, bool) {
	if index < 0 || index >= len(values) || values[index] == nil {
		return "", false
	}

	value, ok := values[index].(string)
	if !ok {
		return "", false
	}

	return value, true
}

func redisValueAsBool(values []interface{}, index int) bool {
	if index < 0 || index >= len(values) {
		return false
	}

	switch value := values[index].(type) {
	case int64:
		return value > 0
	case int:
		return value > 0
	case string:
		return strings.TrimSpace(value) != "" && value != "0"
	default:
		return false
	}
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

func pendingReleaseField(username, operationID string) (string, error) {
	username = strings.TrimSpace(username)
	operationID = strings.TrimSpace(operationID)
	if username == "" || operationID == "" {
		return "", ErrInvalidSessionIdentity
	}
	if _, _, err := ParseUsername(username); err != nil {
		return "", ErrInvalidSessionIdentity
	}

	return base64.RawURLEncoding.EncodeToString([]byte(username)) + ":" +
		base64.RawURLEncoding.EncodeToString([]byte(operationID)), nil
}

func pendingReleaseUserKey(username string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", ErrInvalidSessionIdentity
	}
	if _, _, err := ParseUsername(username); err != nil {
		return "", ErrInvalidSessionIdentity
	}

	return turnPendingReleaseIndexKey + ":" + base64.RawURLEncoding.EncodeToString([]byte(username)), nil
}

func pendingReleaseFieldPrefix(username string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", nil
	}
	if _, _, err := ParseUsername(username); err != nil {
		return "", ErrInvalidSessionIdentity
	}
	return base64.RawURLEncoding.EncodeToString([]byte(username)) + ":", nil
}

func pendingReleaseOperationField(operationID string) (string, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return "", ErrInvalidSessionIdentity
	}

	return base64.RawURLEncoding.EncodeToString([]byte(operationID)), nil
}

func decodePendingReleaseOperationField(field string) (string, error) {
	operationBytes, err := base64.RawURLEncoding.DecodeString(field)
	if err != nil {
		return "", ErrValidationBackend
	}

	operationID := strings.TrimSpace(string(operationBytes))
	if _, err := pendingReleaseOperationField(operationID); err != nil {
		return "", ErrValidationBackend
	}
	return operationID, nil
}

func decodePendingReleaseField(field string) (PendingRelease, error) {
	usernamePart, operationPart, ok := strings.Cut(field, ":")
	if !ok {
		return PendingRelease{}, ErrValidationBackend
	}

	usernameBytes, err := base64.RawURLEncoding.DecodeString(usernamePart)
	if err != nil {
		return PendingRelease{}, ErrValidationBackend
	}
	operationBytes, err := base64.RawURLEncoding.DecodeString(operationPart)
	if err != nil {
		return PendingRelease{}, ErrValidationBackend
	}

	release := PendingRelease{
		Username:    strings.TrimSpace(string(usernameBytes)),
		OperationID: strings.TrimSpace(string(operationBytes)),
	}
	if _, fieldErr := pendingReleaseField(release.Username, release.OperationID); fieldErr != nil {
		return PendingRelease{}, ErrValidationBackend
	}
	return release, nil
}

func PreloadAllocationScripts(ctx context.Context, redisClient redis.UniversalClient) error {
	for _, script := range turnScripts {
		if err := script.Load(ctx, redisClient).Err(); err != nil {
			return err
		}
	}

	return nil
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

func ReleaseAllocationSlot(ctx context.Context, redisClient redis.UniversalClient, username, operationID string) error {
	sessionID, _, err := ParseUsername(username)
	if err != nil {
		return ErrInvalidSessionIdentity
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return ErrInvalidSessionIdentity
	}

	if _, err := releaseAllocationSlotScript.Run(
		ctx,
		redisClient,
		[]string{turnAllocationKey(sessionID)},
		operationID,
		turnAllocationCounterTTL.Milliseconds(),
	).Int(); err != nil {
		return ErrValidationBackend
	}

	return nil
}

func QueuePendingRelease(ctx context.Context, redisClient redis.UniversalClient, username, operationID string) error {
	field, err := pendingReleaseField(username, operationID)
	if err != nil {
		return err
	}
	userKey, err := pendingReleaseUserKey(username)
	if err != nil {
		return err
	}
	operationField, err := pendingReleaseOperationField(operationID)
	if err != nil {
		return err
	}
	if err := redisClient.HSet(ctx, turnPendingReleaseIndexKey, field, "1").Err(); err != nil {
		return ErrValidationBackend
	}
	if err := redisClient.HSet(ctx, userKey, operationField, "1").Err(); err != nil {
		return ErrValidationBackend
	}
	return nil
}

func CompletePendingRelease(ctx context.Context, redisClient redis.UniversalClient, username, operationID string) error {
	field, err := pendingReleaseField(username, operationID)
	if err != nil {
		return err
	}
	userKey, err := pendingReleaseUserKey(username)
	if err != nil {
		return err
	}
	operationField, err := pendingReleaseOperationField(operationID)
	if err != nil {
		return err
	}
	if err := redisClient.HDel(ctx, turnPendingReleaseIndexKey, field).Err(); err != nil {
		return ErrValidationBackend
	}
	if err := redisClient.HDel(ctx, userKey, operationField).Err(); err != nil {
		return ErrValidationBackend
	}
	return nil
}

func PendingReleases(ctx context.Context, redisClient redis.UniversalClient, username string) ([]PendingRelease, error) {
	username = strings.TrimSpace(username)
	if username != "" {
		userKey, err := pendingReleaseUserKey(username)
		if err != nil {
			return nil, err
		}

		fields, err := redisClient.HGetAll(ctx, userKey).Result()
		if err != nil {
			return nil, ErrValidationBackend
		}

		releases, err := pendingReleasesFromUserFields(username, fields)
		if err != nil {
			return nil, err
		}
		return releases, nil
	}

	fields, err := redisClient.HGetAll(ctx, turnPendingReleaseIndexKey).Result()
	if err != nil {
		return nil, ErrValidationBackend
	}
	return pendingReleasesFromGlobalFields(fields, username)
}

func pendingReleasesFromUserFields(username string, fields map[string]string) ([]PendingRelease, error) {
	releases := make([]PendingRelease, 0, len(fields))
	for field := range fields {
		operationID, decodeErr := decodePendingReleaseOperationField(field)
		if decodeErr != nil {
			return nil, decodeErr
		}
		releases = append(releases, PendingRelease{
			Username:    username,
			OperationID: operationID,
		})
	}

	sort.Slice(releases, func(i, j int) bool {
		return releases[i].OperationID < releases[j].OperationID
	})
	return releases, nil
}

func pendingReleasesFromGlobalFields(fields map[string]string, username string) ([]PendingRelease, error) {
	prefix, err := pendingReleaseFieldPrefix(username)
	if err != nil {
		return nil, err
	}

	releases := make([]PendingRelease, 0, len(fields))
	for field := range fields {
		if prefix != "" && !strings.HasPrefix(field, prefix) {
			continue
		}
		release, decodeErr := decodePendingReleaseField(field)
		if decodeErr != nil {
			return nil, decodeErr
		}
		releases = append(releases, release)
	}

	sort.Slice(releases, func(i, j int) bool {
		if releases[i].Username == releases[j].Username {
			return releases[i].OperationID < releases[j].OperationID
		}
		return releases[i].Username < releases[j].Username
	})
	return releases, nil
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

func ValidationErrorSessionIP(err error) string {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.SessionIP()
	}
	return ""
}

func CheckBannedSessionIPs(ctx context.Context, redisClient redis.UniversalClient, sessionIPs []string) ([]string, error) {
	normalized := normalizeSessionIPs(sessionIPs)
	if len(normalized) == 0 {
		return nil, nil
	}

	bannedSet := make(map[string]struct{}, len(normalized))
	if clusterClient, ok := redisClient.(*redis.ClusterClient); ok {
		grouped := groupSessionIPsByClusterSlot(normalized)
		var mu sync.Mutex
		err := runConcurrent(slotBatchTasks(grouped, func(group []string) error {
			banned, batchErr := runBannedSessionIPBatch(ctx, clusterClient, group)
			if batchErr != nil {
				return batchErr
			}
			mu.Lock()
			defer mu.Unlock()
			for _, bannedIP := range banned {
				bannedSet[bannedIP] = struct{}{}
			}
			return nil
		})...)
		if err != nil {
			return nil, ErrValidationBackend
		}
	} else {
		banned, err := runBannedSessionIPBatch(ctx, redisClient, normalized)
		if err != nil {
			return nil, ErrValidationBackend
		}
		for _, bannedIP := range banned {
			bannedSet[bannedIP] = struct{}{}
		}
	}

	banned := make([]string, 0, len(normalized))
	for _, sessionIP := range normalized {
		if _, ok := bannedSet[sessionIP]; ok {
			banned = append(banned, sessionIP)
		}
	}

	return banned, nil
}

func validationErrorWithSessionIP(err error, sessionIP string) error {
	if strings.TrimSpace(sessionIP) == "" {
		return err
	}
	return &ValidationError{cause: err, sessionIP: strings.TrimSpace(sessionIP)}
}

func normalizeSessionIPs(sessionIPs []string) []string {
	if len(sessionIPs) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(sessionIPs))
	seen := make(map[string]struct{}, len(sessionIPs))
	for _, sessionIP := range sessionIPs {
		sessionIP = strings.TrimSpace(sessionIP)
		if sessionIP == "" {
			continue
		}
		if _, ok := seen[sessionIP]; ok {
			continue
		}
		seen[sessionIP] = struct{}{}
		normalized = append(normalized, sessionIP)
	}

	return normalized
}

func runBannedSessionIPBatch(ctx context.Context, redisClient redis.Scripter, sessionIPs []string) ([]string, error) {
	if len(sessionIPs) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(sessionIPs))
	args := make([]interface{}, 0, len(sessionIPs))
	for _, sessionIP := range sessionIPs {
		keys = append(keys, appredis.BanIPKey(sessionIP))
		args = append(args, sessionIP)
	}

	values, err := checkBannedSessionIPsBatchScript.Run(ctx, redisClient, keys, args...).Result()
	if err != nil {
		return nil, err
	}

	rawValues, ok := redisValueSlice(values)
	if !ok {
		return nil, ErrValidationBackend
	}

	banned := make([]string, 0, len(rawValues))
	for _, rawValue := range rawValues {
		value, ok := rawValue.(string)
		if !ok {
			return nil, ErrValidationBackend
		}
		value = strings.TrimSpace(value)
		if value != "" {
			banned = append(banned, value)
		}
	}

	return banned, nil
}

func groupSessionIPsByClusterSlot(sessionIPs []string) map[uint16][]string {
	grouped := make(map[uint16][]string)
	for _, sessionIP := range sessionIPs {
		key := appredis.BanIPKey(sessionIP)
		slot := redisClusterSlot(key)
		grouped[slot] = append(grouped[slot], sessionIP)
	}
	return grouped
}

func slotBatchTasks(grouped map[uint16][]string, fn func([]string) error) []func() error {
	tasks := make([]func() error, 0, len(grouped))
	for _, group := range grouped {
		group := append([]string(nil), group...)
		tasks = append(tasks, func() error {
			return fn(group)
		})
	}
	return tasks
}

func runConcurrent(tasks ...func() error) error {
	if len(tasks) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		wg.Add(1)
		go func(runTask func() error) {
			defer wg.Done()
			if err := runTask(); err != nil {
				errCh <- err
			}
		}(task)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func redisClusterSlot(key string) uint16 {
	hashKey := redisHashSlotKey(key)
	var crc uint16
	for i := 0; i < len(hashKey); i++ {
		crc ^= uint16(hashKey[i]) << 8
		for bit := 0; bit < 8; bit++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
				continue
			}
			crc <<= 1
		}
	}

	return crc % 16384
}

func redisHashSlotKey(key string) string {
	start := strings.IndexByte(key, '{')
	if start < 0 {
		return key
	}
	end := strings.IndexByte(key[start+1:], '}')
	if end <= 0 {
		return key
	}
	return key[start+1 : start+1+end]
}
