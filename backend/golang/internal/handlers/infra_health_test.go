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
