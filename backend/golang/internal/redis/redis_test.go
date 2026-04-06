package redis

import (
	"reflect"
	"testing"
)

func TestRedisClusterAddrsFromEnv(t *testing.T) {
	t.Setenv("REDIS_CLUSTER_NODES", "127.0.0.1:7000, 127.0.0.1:7001 ,127.0.0.1:7002")

	got, err := redisClusterAddrsFromEnv()
	if err != nil {
		t.Fatalf("redisClusterAddrsFromEnv returned error: %v", err)
	}

	want := []string{"127.0.0.1:7000", "127.0.0.1:7001", "127.0.0.1:7002"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("redisClusterAddrsFromEnv() = %#v, want %#v", got, want)
	}
}

func TestRedisClusterAddrsFromEnvRequiresClusterNodes(t *testing.T) {
	t.Setenv("REDIS_CLUSTER_NODES", "")

	_, err := redisClusterAddrsFromEnv()
	if err == nil {
		t.Fatal("redisClusterAddrsFromEnv should require REDIS_CLUSTER_NODES")
	}
}

func TestRedisClusterAddrsFromEnvRejectsBlankEntries(t *testing.T) {
	t.Setenv("REDIS_CLUSTER_NODES", " , , ")

	_, err := redisClusterAddrsFromEnv()
	if err == nil {
		t.Fatal("redisClusterAddrsFromEnv should reject blank REDIS_CLUSTER_NODES")
	}
}
