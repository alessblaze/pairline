package turnservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/redis/go-redis/v9"
)

type fakeRedisClient struct {
	redis.UniversalClient
	strings   map[string]string
	exists    map[string]bool
	ints      map[string]int64
	expiry    map[string]time.Duration
	scriptSHA map[string]string
	getErr    map[string]error
	existsErr map[string]error
	incrErr   map[string]error
	decrErr   map[string]error
	delErr    map[string]error
}

func newFakeRedisClient() *fakeRedisClient {
	client := &fakeRedisClient{
		strings:   map[string]string{},
		exists:    map[string]bool{},
		ints:      map[string]int64{},
		expiry:    map[string]time.Duration{},
		scriptSHA: map[string]string{},
		getErr:    map[string]error{},
		existsErr: map[string]error{},
		incrErr:   map[string]error{},
		decrErr:   map[string]error{},
		delErr:    map[string]error{},
	}
	client.scriptSHA[reserveAllocationSlotScript.Hash()] = reserveAllocationSlotScriptSource
	client.scriptSHA[releaseAllocationSlotScript.Hash()] = releaseAllocationSlotScriptSource
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
	}
	return redis.NewIntResult(int64(len(keys)), nil)
}

func (f *fakeRedisClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
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
			if err, ok := f.decrErr[key]; ok {
				return redis.NewCmdResult(nil, err)
			}
			f.ints[key]--
			if f.ints[key] <= 0 {
				delete(f.ints, key)
				delete(f.expiry, key)
			}
			return redis.NewCmdResult(int64(0), nil)
		}
		return redis.NewCmdResult(int64(1), nil)

	case releaseAllocationSlotScriptSource:
		if len(keys) != 1 {
			return redis.NewCmdResult(nil, fmt.Errorf("unexpected release script args"))
		}

		key := keys[0]
		if err, ok := f.decrErr[key]; ok {
			return redis.NewCmdResult(nil, err)
		}

		f.ints[key]--
		if f.ints[key] <= 0 {
			delete(f.ints, key)
			delete(f.expiry, key)
			return redis.NewCmdResult(int64(0), nil)
		}
		return redis.NewCmdResult(f.ints[key], nil)
	default:
		return redis.NewCmdResult(nil, fmt.Errorf("unexpected script"))
	}
}

func (f *fakeRedisClient) EvalSha(ctx context.Context, sha1 string, keys []string, args ...interface{}) *redis.Cmd {
	script, ok := f.scriptSHA[sha1]
	if !ok {
		return redis.NewCmdResult(nil, redis.Nil)
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

	if err := ReleaseAllocationSlot(context.Background(), redisClient, username); err != nil {
		t.Fatalf("ReleaseAllocationSlot() error = %v", err)
	}

	if got := redisClient.ints[turnAllocationKey("session-1")]; got != 0 {
		t.Fatalf("allocation counter = %d, want 0", got)
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
