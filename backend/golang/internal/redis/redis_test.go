// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

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
