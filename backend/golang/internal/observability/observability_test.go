package observability

import "testing"

func TestMetricsEnabledUsesMetricsOrGenericEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	if metricsEnabled() {
		t.Fatal("metricsEnabled() should be false when no metrics endpoint is configured")
	}

	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://collector:4318")
	if !metricsEnabled() {
		t.Fatal("metricsEnabled() should be true when metrics endpoint is configured")
	}
}

func TestTracingEnabledUsesTracingOrGenericEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	if tracingEnabled() {
		t.Fatal("tracingEnabled() should be false when no tracing endpoint is configured")
	}

	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://collector:4318")
	if !tracingEnabled() {
		t.Fatal("tracingEnabled() should be true when traces endpoint is configured")
	}
}

func TestTelemetryEnabledIncludesMetricsEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	if telemetryEnabled() {
		t.Fatal("telemetryEnabled() should be false when no endpoints are configured")
	}

	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://collector:4318")
	if !telemetryEnabled() {
		t.Fatal("telemetryEnabled() should be true when metrics endpoint is configured")
	}
}

func TestEnvOrDefaultAndServiceInstanceID(t *testing.T) {
	t.Setenv("PAIRLINE_TEST_ENV_OR_DEFAULT", "configured")
	if got := envOrDefault("PAIRLINE_TEST_ENV_OR_DEFAULT", "fallback"); got != "configured" {
		t.Fatalf("envOrDefault() = %q, want %q", got, "configured")
	}

	t.Setenv("PAIRLINE_TEST_ENV_OR_DEFAULT", "")
	if got := envOrDefault("PAIRLINE_TEST_ENV_OR_DEFAULT", "fallback"); got != "fallback" {
		t.Fatalf("envOrDefault() = %q, want %q", got, "fallback")
	}

	t.Setenv("OTEL_SERVICE_INSTANCE_ID", "instance-123")
	if got := serviceInstanceID(); got != "instance-123" {
		t.Fatalf("serviceInstanceID() = %q, want %q", got, "instance-123")
	}
}
