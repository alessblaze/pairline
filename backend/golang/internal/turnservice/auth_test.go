package turnservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/redis/go-redis/v9"
)

type fakeRedisError string

func (e fakeRedisError) Error() string {
	return string(e)
}

func (fakeRedisError) RedisError() {}

type fakeRedisClient struct {
	redis.UniversalClient
	strings    map[string]string
	hashes     map[string]map[string]string
	exists     map[string]bool
	ints       map[string]int64
	expiry     map[string]time.Duration
	releaseOps map[string]map[string]bool
	scriptSHA  map[string]string
	getErr     map[string]error
	existsErr  map[string]error
	incrErr    map[string]error
	decrErr    map[string]error
	delErr     map[string]error
	hsetErr    map[string]error
	hdelErr    map[string]error
}

func newFakeRedisClient() *fakeRedisClient {
	client := &fakeRedisClient{
		strings:    map[string]string{},
		hashes:     map[string]map[string]string{},
		exists:     map[string]bool{},
		ints:       map[string]int64{},
		expiry:     map[string]time.Duration{},
		releaseOps: map[string]map[string]bool{},
		scriptSHA:  map[string]string{},
		getErr:     map[string]error{},
		existsErr:  map[string]error{},
		incrErr:    map[string]error{},
		decrErr:    map[string]error{},
		delErr:     map[string]error{},
		hsetErr:    map[string]error{},
		hdelErr:    map[string]error{},
	}
	return client
}

func (f *fakeRedisClient) Get(_ context.Context, key string) *redis.StringCmd {
	if err, ok := f.getErr[key]; ok {
		return redis.NewStringResult("", err)
	}
	value, ok := f.strings[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(value, nil)
}

func (f *fakeRedisClient) Exists(_ context.Context, keys ...string) *redis.IntCmd {
	for _, key := range keys {
		if err, ok := f.existsErr[key]; ok {
			return redis.NewIntResult(0, err)
		}
	}

	var count int64
	for _, key := range keys {
		if f.exists[key] {
			count++
		}
	}
	return redis.NewIntResult(count, nil)
}

func (f *fakeRedisClient) Incr(_ context.Context, key string) *redis.IntCmd {
	if err, ok := f.incrErr[key]; ok {
		return redis.NewIntResult(0, err)
	}
	f.ints[key]++
	return redis.NewIntResult(f.ints[key], nil)
}

func (f *fakeRedisClient) Decr(_ context.Context, key string) *redis.IntCmd {
	if err, ok := f.decrErr[key]; ok {
		return redis.NewIntResult(0, err)
	}
	f.ints[key]--
	return redis.NewIntResult(f.ints[key], nil)
}

func (f *fakeRedisClient) Del(_ context.Context, keys ...string) *redis.IntCmd {
	for _, key := range keys {
		if err, ok := f.delErr[key]; ok {
			return redis.NewIntResult(0, err)
		}
		delete(f.ints, key)
		delete(f.expiry, key)
		delete(f.releaseOps, key)
		delete(f.hashes, key)
		delete(f.strings, key)
	}
	return redis.NewIntResult(int64(len(keys)), nil)
}

func (f *fakeRedisClient) HSet(_ context.Context, key string, values ...interface{}) *redis.IntCmd {
	if err, ok := f.hsetErr[key]; ok {
		return redis.NewIntResult(0, err)
	}
	if len(values)%2 != 0 {
		return redis.NewIntResult(0, fmt.Errorf("unexpected hset values"))
	}
	if f.hashes[key] == nil {
		f.hashes[key] = make(map[string]string)
	}

	var added int64
	for i := 0; i < len(values); i += 2 {
		field, ok := values[i].(string)
		if !ok {
			return redis.NewIntResult(0, fmt.Errorf("invalid hset field %v", values[i]))
		}
		value, ok := values[i+1].(string)
		if !ok {
			return redis.NewIntResult(0, fmt.Errorf("invalid hset value %v", values[i+1]))
		}
		if _, exists := f.hashes[key][field]; !exists {
			added++
		}
		f.hashes[key][field] = value
	}
	return redis.NewIntResult(added, nil)
}

func (f *fakeRedisClient) HDel(_ context.Context, key string, fields ...string) *redis.IntCmd {
	if err, ok := f.hdelErr[key]; ok {
		return redis.NewIntResult(0, err)
	}
	if f.hashes[key] == nil {
		return redis.NewIntResult(0, nil)
	}

	var removed int64
	for _, field := range fields {
		if _, exists := f.hashes[key][field]; exists {
			delete(f.hashes[key], field)
			removed++
		}
	}
	if len(f.hashes[key]) == 0 {
		delete(f.hashes, key)
	}
	return redis.NewIntResult(removed, nil)
}

func (f *fakeRedisClient) HGetAll(_ context.Context, key string) *redis.MapStringStringCmd {
	values := make(map[string]string, len(f.hashes[key]))
	for field, value := range f.hashes[key] {
		values[field] = value
	}
	return redis.NewMapStringStringResult(values, nil)
}

func (f *fakeRedisClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	f.scriptSHA[redis.NewScript(script).Hash()] = script

	switch script {
	case reserveAllocationSlotScriptSource:
		if len(keys) != 1 || len(args) != 2 {
			return redis.NewCmdResult(nil, fmt.Errorf("unexpected reserve script args"))
		}

		key := keys[0]
		if err, ok := f.incrErr[key]; ok {
			return redis.NewCmdResult(nil, err)
		}

		limit, ok := toInt64(args[0])
		if !ok {
			return redis.NewCmdResult(nil, fmt.Errorf("invalid limit arg %v", args[0]))
		}
		ttlMs, ok := toInt64(args[1])
		if !ok {
			return redis.NewCmdResult(nil, fmt.Errorf("invalid ttl arg %v", args[1]))
		}

		f.ints[key]++
		f.expiry[key] = time.Duration(ttlMs) * time.Millisecond
		if f.ints[key] > limit {
			f.ints[key]--
			if f.ints[key] <= 0 {
				delete(f.ints, key)
				delete(f.expiry, key)
			}
			return redis.NewCmdResult(int64(0), nil)
		}
		return redis.NewCmdResult(int64(1), nil)

	case releaseAllocationSlotScriptSource:
		if len(keys) != 1 || len(args) != 2 {
			return redis.NewCmdResult(nil, fmt.Errorf("unexpected release script args"))
		}

		key := keys[0]
		operationID, ok := args[0].(string)
		if !ok || operationID == "" {
			return redis.NewCmdResult(nil, fmt.Errorf("invalid release operation id %v", args[0]))
		}
		ttlMs, ok := toInt64(args[1])
		if !ok {
			return redis.NewCmdResult(nil, fmt.Errorf("invalid ttl arg %v", args[1]))
		}

		if f.releaseOps[key] == nil {
			f.releaseOps[key] = make(map[string]bool)
		}
		if f.ints[key] <= 0 {
			f.ints[key] = 0
			f.releaseOps[key][operationID] = true
			f.expiry[key] = time.Duration(ttlMs) * time.Millisecond
			return redis.NewCmdResult(int64(0), nil)
		}
		if f.releaseOps[key][operationID] {
			return redis.NewCmdResult(f.ints[key], nil)
		}

		if err, ok := f.decrErr[key]; ok {
			return redis.NewCmdResult(nil, err)
		}

		f.releaseOps[key][operationID] = true
		f.ints[key]--
		if f.ints[key] <= 0 {
			f.ints[key] = 0
			f.expiry[key] = time.Duration(ttlMs) * time.Millisecond
			return redis.NewCmdResult(int64(0), nil)
		}
		f.expiry[key] = time.Duration(ttlMs) * time.Millisecond
		return redis.NewCmdResult(f.ints[key], nil)
	case sessionValidationSnapshotScriptSource:
		if len(keys) != 4 {
			return redis.NewCmdResult(nil, fmt.Errorf("unexpected session validation script args"))
		}
		for _, key := range keys {
			if err, ok := f.getErr[key]; ok {
				return redis.NewCmdResult(nil, err)
			}
		}
		expectedToken := f.strings[keys[0]]
		var sessionExists int64
		if f.exists[keys[1]] {
			sessionExists = 1
		}
		sessionIP := f.strings[keys[2]]
		matchedID := f.strings[keys[3]]
		return redis.NewCmdResult([]interface{}{expectedToken, sessionExists, sessionIP, matchedID}, nil)
	case peerValidationSnapshotScriptSource:
		if len(keys) != 2 {
			return redis.NewCmdResult(nil, fmt.Errorf("unexpected peer validation script args"))
		}
		for _, key := range keys {
			if err, ok := f.getErr[key]; ok {
				return redis.NewCmdResult(nil, err)
			}
		}
		var peerExists int64
		if f.exists[keys[0]] {
			peerExists = 1
		}
		peerMatchedID := f.strings[keys[1]]
		return redis.NewCmdResult([]interface{}{peerExists, peerMatchedID}, nil)
	case checkBannedSessionIPsBatchScriptSource:
		if len(keys) != len(args) {
			return redis.NewCmdResult(nil, fmt.Errorf("unexpected banned session IP batch args"))
		}
		banned := make([]interface{}, 0, len(keys))
		for index, key := range keys {
			if err, ok := f.existsErr[key]; ok {
				return redis.NewCmdResult(nil, err)
			}
			if !f.exists[key] {
				continue
			}
			ipAddress, ok := args[index].(string)
			if !ok {
				return redis.NewCmdResult(nil, fmt.Errorf("invalid session ip arg %v", args[index]))
			}
			banned = append(banned, ipAddress)
		}
		return redis.NewCmdResult(banned, nil)
	default:
		return redis.NewCmdResult(nil, fmt.Errorf("unexpected script"))
	}
}

func (f *fakeRedisClient) EvalSha(ctx context.Context, sha1 string, keys []string, args ...interface{}) *redis.Cmd {
	script, ok := f.scriptSHA[sha1]
	if !ok {
		return redis.NewCmdResult(nil, fakeRedisError("NOSCRIPT No matching script."))
	}
	return f.Eval(ctx, script, keys, args...)
}

func (f *fakeRedisClient) ScriptLoad(_ context.Context, script string) *redis.StringCmd {
	sha := redis.NewScript(script).Hash()
	f.scriptSHA[sha] = script
	return redis.NewStringResult(sha, nil)
}

func TestValidateMatchedSessionRejectsBanLookupErrors(t *testing.T) {
	route := appredis.SessionRoute{Mode: "video", Shard: 3}
	redisClient := newFakeRedisClient()
	seedMatchedSession(redisClient, route, "session-1", "token-1", "session-2")
	seedPeerSession(redisClient, route, "session-2", "session-1")
	redisClient.existsErr[appredis.BanSessionKey("session-1")] = errors.New("redis down")

	_, err := ValidateMatchedSession(context.Background(), redisClient, "session-1", "token-1")
	if !errors.Is(err, ErrValidationBackend) {
		t.Fatalf("ValidateMatchedSession() error = %v, want %v", err, ErrValidationBackend)
	}
}

func TestValidateMatchedSessionIncludesSessionIPOnBanErrors(t *testing.T) {
	route := appredis.SessionRoute{Mode: "video", Shard: 3}
	redisClient := newFakeRedisClient()
	seedMatchedSession(redisClient, route, "session-1", "token-1", "session-2")
	seedPeerSession(redisClient, route, "session-2", "session-1")
	redisClient.strings[appredis.SessionIPKey("session-1", route)] = "203.0.113.24"
	redisClient.exists[appredis.BanSessionKey("session-1")] = true

	_, err := ValidateMatchedSession(context.Background(), redisClient, "session-1", "token-1")
	if !errors.Is(err, ErrSessionBanned) {
		t.Fatalf("ValidateMatchedSession() error = %v, want %v", err, ErrSessionBanned)
	}
	if got := ValidationErrorSessionIP(err); got != "203.0.113.24" {
		t.Fatalf("ValidationErrorSessionIP() = %q, want %q", got, "203.0.113.24")
	}
}

func TestValidateMatchedSessionPrefersSessionBanOverMissingPeerRoute(t *testing.T) {
	route := appredis.SessionRoute{Mode: "video", Shard: 3}
	redisClient := newFakeRedisClient()
	seedMatchedSession(redisClient, route, "session-1", "token-1", "session-2")
	redisClient.strings[appredis.SessionIPKey("session-1", route)] = "203.0.113.24"
	redisClient.exists[appredis.BanIPKey("203.0.113.24")] = true

	_, err := ValidateMatchedSession(context.Background(), redisClient, "session-1", "token-1")
	if !errors.Is(err, ErrSessionBanned) {
		t.Fatalf("ValidateMatchedSession() error = %v, want %v", err, ErrSessionBanned)
	}
	if got := ValidationErrorSessionIP(err); got != "203.0.113.24" {
		t.Fatalf("ValidationErrorSessionIP() = %q, want %q", got, "203.0.113.24")
	}
}

func TestCheckBannedSessionIPs(t *testing.T) {
	redisClient := newFakeRedisClient()
	redisClient.exists[appredis.BanIPKey("203.0.113.24")] = true

	got, err := CheckBannedSessionIPs(context.Background(), redisClient, []string{
		"203.0.113.24",
		"198.51.100.8",
		"203.0.113.24",
		" ",
	})
	if err != nil {
		t.Fatalf("CheckBannedSessionIPs() error = %v", err)
	}
	if len(got) != 1 || got[0] != "203.0.113.24" {
		t.Fatalf("CheckBannedSessionIPs() = %#v, want only banned IP", got)
	}
}

func TestValidateMatchedSessionRejectsNonReciprocalMatches(t *testing.T) {
	route := appredis.SessionRoute{Mode: "video", Shard: 3}
	redisClient := newFakeRedisClient()
	seedMatchedSession(redisClient, route, "session-1", "token-1", "session-2")
	seedPeerSession(redisClient, route, "session-2", "session-3")

	_, err := ValidateMatchedSession(context.Background(), redisClient, "session-1", "token-1")
	if !errors.Is(err, ErrSessionUnmatched) {
		t.Fatalf("ValidateMatchedSession() error = %v, want %v", err, ErrSessionUnmatched)
	}
}

func TestReserveAndReleaseAllocationSlot(t *testing.T) {
	redisClient := newFakeRedisClient()
	username := BuildUsername("session-1", "token-1")

	allowed, err := ReserveAllocationSlot(context.Background(), redisClient, username, 1)
	if err != nil {
		t.Fatalf("ReserveAllocationSlot() error = %v", err)
	}
	if !allowed {
		t.Fatal("ReserveAllocationSlot() = false, want true")
	}

	allowed, err = ReserveAllocationSlot(context.Background(), redisClient, username, 1)
	if err != nil {
		t.Fatalf("ReserveAllocationSlot(second) error = %v", err)
	}
	if allowed {
		t.Fatal("ReserveAllocationSlot(second) = true, want false")
	}

	if err := ReleaseAllocationSlot(context.Background(), redisClient, username, "release-1"); err != nil {
		t.Fatalf("ReleaseAllocationSlot() error = %v", err)
	}

	if got := redisClient.ints[turnAllocationKey("session-1")]; got != 0 {
		t.Fatalf("allocation counter = %d, want 0", got)
	}
}

func TestPreloadAllocationScriptsLoadsScriptHashes(t *testing.T) {
	redisClient := newFakeRedisClient()

	if err := PreloadAllocationScripts(context.Background(), redisClient); err != nil {
		t.Fatalf("PreloadAllocationScripts() error = %v", err)
	}

	for _, script := range turnScripts {
		if _, ok := redisClient.scriptSHA[script.Hash()]; !ok {
			t.Fatalf("script %s was not preloaded", script.Hash())
		}
	}
}

func TestReserveAllocationSlotRecoversFromNOSCRIPT(t *testing.T) {
	redisClient := newFakeRedisClient()
	username := BuildUsername("session-1", "token-1")

	allowed, err := ReserveAllocationSlot(context.Background(), redisClient, username, 1)
	if err != nil {
		t.Fatalf("ReserveAllocationSlot() error = %v", err)
	}
	if !allowed {
		t.Fatal("ReserveAllocationSlot() = false, want true")
	}
	if _, ok := redisClient.scriptSHA[reserveAllocationSlotScript.Hash()]; !ok {
		t.Fatalf("reserve allocation script hash %s was not cached after NOSCRIPT fallback", reserveAllocationSlotScript.Hash())
	}
}

func TestReserveAllocationSlotSetsSafetyTTL(t *testing.T) {
	redisClient := newFakeRedisClient()
	username := BuildUsername("session-1", "token-1")

	allowed, err := ReserveAllocationSlot(context.Background(), redisClient, username, 2)
	if err != nil {
		t.Fatalf("ReserveAllocationSlot() error = %v", err)
	}
	if !allowed {
		t.Fatal("ReserveAllocationSlot() = false, want true")
	}

	if got := redisClient.expiry[turnAllocationKey("session-1")]; got != turnAllocationCounterTTL {
		t.Fatalf("allocation ttl = %s, want %s", got, turnAllocationCounterTTL)
	}
}

func TestReleaseAllocationSlotIsIdempotentPerOperationID(t *testing.T) {
	redisClient := newFakeRedisClient()
	username := BuildUsername("session-1", "token-1")

	allowed, err := ReserveAllocationSlot(context.Background(), redisClient, username, 2)
	if err != nil {
		t.Fatalf("ReserveAllocationSlot() error = %v", err)
	}
	if !allowed {
		t.Fatal("ReserveAllocationSlot() = false, want true")
	}

	allowed, err = ReserveAllocationSlot(context.Background(), redisClient, username, 2)
	if err != nil {
		t.Fatalf("ReserveAllocationSlot(second) error = %v", err)
	}
	if !allowed {
		t.Fatal("ReserveAllocationSlot(second) = false, want true")
	}

	if err := ReleaseAllocationSlot(context.Background(), redisClient, username, "release-1"); err != nil {
		t.Fatalf("ReleaseAllocationSlot(first) error = %v", err)
	}
	if err := ReleaseAllocationSlot(context.Background(), redisClient, username, "release-1"); err != nil {
		t.Fatalf("ReleaseAllocationSlot(retry) error = %v", err)
	}
	if got := redisClient.ints[turnAllocationKey("session-1")]; got != 1 {
		t.Fatalf("allocation counter after duplicate release = %d, want 1", got)
	}

	if err := ReleaseAllocationSlot(context.Background(), redisClient, username, "release-2"); err != nil {
		t.Fatalf("ReleaseAllocationSlot(second allocation) error = %v", err)
	}
	if got := redisClient.ints[turnAllocationKey("session-1")]; got != 0 {
		t.Fatalf("allocation counter after distinct release = %d, want 0", got)
	}
}

func TestReleaseAllocationSlotDoesNotTouchNewAllocationGeneration(t *testing.T) {
	redisClient := newFakeRedisClient()
	username := BuildUsername("session-1", "token-1")

	allowed, err := ReserveAllocationSlot(context.Background(), redisClient, username, 1)
	if err != nil {
		t.Fatalf("ReserveAllocationSlot() error = %v", err)
	}
	if !allowed {
		t.Fatal("ReserveAllocationSlot() = false, want true")
	}

	if err := ReleaseAllocationSlot(context.Background(), redisClient, username, "release-1"); err != nil {
		t.Fatalf("ReleaseAllocationSlot(first generation) error = %v", err)
	}

	allowed, err = ReserveAllocationSlot(context.Background(), redisClient, username, 1)
	if err != nil {
		t.Fatalf("ReserveAllocationSlot(second generation) error = %v", err)
	}
	if !allowed {
		t.Fatal("ReserveAllocationSlot(second generation) = false, want true")
	}

	if err := ReleaseAllocationSlot(context.Background(), redisClient, username, "release-1"); err != nil {
		t.Fatalf("ReleaseAllocationSlot(retry) error = %v", err)
	}
	if got := redisClient.ints[turnAllocationKey("session-1")]; got != 1 {
		t.Fatalf("allocation counter after retry against new generation = %d, want 1", got)
	}
}

func TestReleaseAllocationSlotOnMissingKeyStillProtectsFutureGeneration(t *testing.T) {
	redisClient := newFakeRedisClient()
	username := BuildUsername("session-1", "token-1")

	if err := ReleaseAllocationSlot(context.Background(), redisClient, username, "release-1"); err != nil {
		t.Fatalf("ReleaseAllocationSlot(missing key) error = %v", err)
	}
	if got := redisClient.ints[turnAllocationKey("session-1")]; got != 0 {
		t.Fatalf("allocation counter after missing-key release = %d, want 0", got)
	}

	allowed, err := ReserveAllocationSlot(context.Background(), redisClient, username, 1)
	if err != nil {
		t.Fatalf("ReserveAllocationSlot() error = %v", err)
	}
	if !allowed {
		t.Fatal("ReserveAllocationSlot() = false, want true")
	}

	if err := ReleaseAllocationSlot(context.Background(), redisClient, username, "release-1"); err != nil {
		t.Fatalf("ReleaseAllocationSlot(retry) error = %v", err)
	}
	if got := redisClient.ints[turnAllocationKey("session-1")]; got != 1 {
		t.Fatalf("allocation counter after missing-key retry against new generation = %d, want 1", got)
	}
}

func TestPendingReleasesRoundTrip(t *testing.T) {
	redisClient := newFakeRedisClient()
	firstUsername := BuildUsername("session-1", "token-1")
	secondUsername := BuildUsername("session-2", "token-2")

	if err := QueuePendingRelease(context.Background(), redisClient, secondUsername, "release-2"); err != nil {
		t.Fatalf("QueuePendingRelease(second) error = %v", err)
	}
	if err := QueuePendingRelease(context.Background(), redisClient, firstUsername, "release-1"); err != nil {
		t.Fatalf("QueuePendingRelease(first) error = %v", err)
	}

	filtered, err := PendingReleases(context.Background(), redisClient, firstUsername)
	if err != nil {
		t.Fatalf("PendingReleases(filtered) error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].OperationID != "release-1" {
		t.Fatalf("PendingReleases(filtered) = %#v, want only release-1", filtered)
	}

	all, err := PendingReleases(context.Background(), redisClient, "")
	if err != nil {
		t.Fatalf("PendingReleases(all) error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(PendingReleases(all)) = %d, want 2", len(all))
	}

	if err := CompletePendingRelease(context.Background(), redisClient, firstUsername, "release-1"); err != nil {
		t.Fatalf("CompletePendingRelease() error = %v", err)
	}
	all, err = PendingReleases(context.Background(), redisClient, "")
	if err != nil {
		t.Fatalf("PendingReleases(after complete) error = %v", err)
	}
	if len(all) != 1 || all[0].OperationID != "release-2" {
		t.Fatalf("PendingReleases(after complete) = %#v, want only release-2", all)
	}

	userKey, err := pendingReleaseUserKey(firstUsername)
	if err != nil {
		t.Fatalf("pendingReleaseUserKey() error = %v", err)
	}
	if _, ok := redisClient.hashes[userKey]; ok {
		t.Fatalf("user-specific pending release index still exists after completion: %#v", redisClient.hashes[userKey])
	}
}

func TestRunConcurrentJoinsMultipleErrors(t *testing.T) {
	err := runConcurrent(
		func() error { return errors.New("first failure") },
		func() error { return errors.New("second failure") },
	)
	if err == nil {
		t.Fatal("runConcurrent() error = nil, want joined error")
	}
	if !strings.Contains(err.Error(), "first failure") {
		t.Fatalf("runConcurrent() error = %q, want first failure", err.Error())
	}
	if !strings.Contains(err.Error(), "second failure") {
		t.Fatalf("runConcurrent() error = %q, want second failure", err.Error())
	}
}

func pendingReleaseUserKeyMust(t *testing.T, username string) string {
	t.Helper()

	userKey, err := pendingReleaseUserKey(username)
	if err != nil {
		t.Fatalf("pendingReleaseUserKey() error = %v", err)
	}
	return userKey
}

func toInt64(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case int32:
		return int64(v), true
	default:
		return 0, false
	}
}

func seedMatchedSession(redisClient *fakeRedisClient, route appredis.SessionRoute, sessionID, rawToken, matchedID string) {
	tokenHash := sha256.Sum256([]byte(rawToken))
	redisClient.strings[appredis.SessionLocatorKey(sessionID)] = route.Mode + "|" + strconv.Itoa(route.Shard)
	redisClient.strings[appredis.SessionTokenKey(sessionID, route)] = hex.EncodeToString(tokenHash[:])
	redisClient.exists[appredis.SessionDataKey(sessionID, route)] = true
	redisClient.strings[appredis.MatchKey(sessionID, route)] = matchedID
}

func seedPeerSession(redisClient *fakeRedisClient, route appredis.SessionRoute, sessionID, matchedID string) {
	redisClient.strings[appredis.SessionLocatorKey(sessionID)] = route.Mode + "|" + strconv.Itoa(route.Shard)
	redisClient.exists[appredis.SessionDataKey(sessionID, route)] = true
	redisClient.strings[appredis.MatchKey(sessionID, route)] = matchedID
}
