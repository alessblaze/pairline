package handlers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestPingCollectorHealthURLAcceptsPlainTextSuccess(t *testing.T) {
	previousClient := infraHTTPClient
	infraHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader("Server available")),
				Header:     make(http.Header),
			}, nil
		}),
	}
	t.Cleanup(func() {
		infraHTTPClient = previousClient
	})

	health := pingCollectorHealthURL(context.Background(), "http://collector.example/health")

	if health.Status != "ok" {
		t.Fatalf("pingCollectorHealthURL() status = %q, want %q", health.Status, "ok")
	}
	if health.Error != "" {
		t.Fatalf("pingCollectorHealthURL() error = %q, want empty", health.Error)
	}
	if health.LatencyMS < 1 {
		t.Fatalf("pingCollectorHealthURL() latency = %d, want at least 1", health.LatencyMS)
	}
}

func TestFetchConfiguredServiceHealthProbesTargetsConcurrently(t *testing.T) {
	t.Setenv("SHARED_SECRET", "shared-secret")
	previousClient := infraHTTPClient
	infraHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			time.Sleep(200 * time.Millisecond)
			if strings.Contains(req.URL.String(), "phoenix") && req.Header.Get("x-shared-secret") != "shared-secret" {
				return &http.Response{
					StatusCode: http.StatusUnauthorized,
					Status:     "401 Unauthorized",
					Body:       io.NopCloser(strings.NewReader(`{"status":"error","error":"missing shared secret"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			}

			service := "go"
			if strings.Contains(req.URL.String(), "phoenix") {
				service = "phoenix"
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"status":"ok","service":"` + service + `","timestamp":1}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}
	t.Cleanup(func() {
		infraHTTPClient = previousClient
	})

	t.Setenv("ADMIN_HEALTH_PHOENIX_URLS", strings.Join([]string{
		"http://phoenix-one.internal/api/health",
		"http://phoenix-two.internal/api/health",
	}, ","))
	t.Setenv("ADMIN_HEALTH_GO_URLS", "http://go-one.internal/health")

	startedAt := time.Now()
	services := fetchConfiguredServiceHealth(context.Background())
	elapsed := time.Since(startedAt)

	if len(services) != 3 {
		t.Fatalf("fetchConfiguredServiceHealth() count = %d, want %d", len(services), 3)
	}
	if elapsed >= 450*time.Millisecond {
		t.Fatalf("fetchConfiguredServiceHealth() took %v, want concurrent execution under 450ms", elapsed)
	}
	for _, service := range services {
		if service.Status != "ok" {
			t.Fatalf("service %q status = %q, want ok", service.URL, service.Status)
		}
		if service.LatencyMS < 1 {
			t.Fatalf("service %q latency = %d, want at least 1", service.URL, service.LatencyMS)
		}
	}
}

func TestDurationMillisCeilRoundsSubMillisecondDurationsUp(t *testing.T) {
	if got := durationMillisCeil(500 * time.Microsecond); got != 1 {
		t.Fatalf("durationMillisCeil(500µs) = %d, want 1", got)
	}
	if got := durationMillisCeil(3 * time.Millisecond); got != 3 {
		t.Fatalf("durationMillisCeil(3ms) = %d, want 3", got)
	}
}

func TestParseRedisClusterNodesParsesRolesAndStatuses(t *testing.T) {
	raw := strings.Join([]string{
		"07c37dfeb2352e0b490f7e1daa9a0b6f3b8f85f4 172.30.0.21:7001@17001 master - 0 1710000000000 1 connected 0-5460",
		"2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a 172.30.0.24:7004@17004 slave 07c37dfeb2352e0b490f7e1daa9a0b6f3b8f85f4 0 1710000000001 4 connected",
		"3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b 172.30.0.22:7002@17002 master - 0 1710000000002 2 disconnected 5461-10922",
		"4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c 172.30.0.25:7005@17005 slave 3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b 0 1710000000003 5 fail?",
	}, "\n")

	nodes := parseRedisClusterNodes(raw)
	if len(nodes) != 4 {
		t.Fatalf("parseRedisClusterNodes() count = %d, want 4", len(nodes))
	}

	if nodes[0].Role != "master" || nodes[0].Status != "ok" {
		t.Fatalf("first node role/status = %q/%q, want master/ok", nodes[0].Role, nodes[0].Status)
	}
	if nodes[1].Role != "master" || nodes[1].Status != "degraded" {
		t.Fatalf("second node role/status = %q/%q, want master/degraded", nodes[1].Role, nodes[1].Status)
	}
	if nodes[2].Role != "replica" || nodes[2].MasterID == "" || nodes[2].Status != "ok" {
		t.Fatalf("third node = %+v, want connected replica with master id", nodes[2])
	}
	if nodes[3].Status != "degraded" {
		t.Fatalf("fourth node status = %q, want degraded", nodes[3].Status)
	}
}

func TestParseRedisClusterInfoParsesIntegers(t *testing.T) {
	info := parseRedisClusterInfo(strings.Join([]string{
		"cluster_state:ok",
		"cluster_slots_assigned:16384",
		"cluster_slots_ok:16384",
		"cluster_slots_pfail:0",
		"cluster_slots_fail:0",
		"cluster_known_nodes:6",
		"cluster_size:3",
	}, "\n"))

	if info["cluster_state"] != "ok" {
		t.Fatalf("cluster_state = %q, want ok", info["cluster_state"])
	}
	if got := redisClusterInfoInt(info, "cluster_known_nodes"); got != 6 {
		t.Fatalf("cluster_known_nodes = %d, want 6", got)
	}
	if got := redisClusterInfoInt(info, "cluster_slots_assigned"); got != 16384 {
		t.Fatalf("cluster_slots_assigned = %d, want 16384", got)
	}
}

func TestParseRedisCommandStatsSortsByCalls(t *testing.T) {
	raw := strings.Join([]string{
		"# Commandstats",
		"cmdstat_ping:calls=18637,usec=28044,usec_per_call=1.50",
		"cmdstat_publish:calls=9524,usec=81760,usec_per_call=8.58",
		"cmdstat_info:calls=5841,usec=374959,usec_per_call=64.19",
	}, "\n")

	stats := parseRedisCommandStats(raw)
	if len(stats) != 3 {
		t.Fatalf("parseRedisCommandStats() count = %d, want 3", len(stats))
	}
	if stats[0].Command != "ping" || stats[0].Calls != 18637 {
		t.Fatalf("first stat = %+v, want ping sorted first", stats[0])
	}
	if stats[1].UsecPerCall != 8.58 {
		t.Fatalf("second stat usec_per_call = %v, want 8.58", stats[1].UsecPerCall)
	}
}
