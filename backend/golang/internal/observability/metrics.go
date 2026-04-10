package observability

import (
	"context"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var (
	meter = noop.NewMeterProvider().Meter("pairline/go")

	httpRequestsTotal        metric.Int64Counter
	httpRequestDuration      metric.Float64Histogram
	httpInflightRequests     metric.Int64UpDownCounter
	businessEventsTotal      metric.Int64Counter
	webRTCActiveConnections  metric.Int64UpDownCounter
	webRTCConnectionDuration metric.Float64Histogram
	webRTCDisconnectsTotal   metric.Int64Counter
	turnRequestsTotal        metric.Int64Counter
	turnRequestDuration      metric.Float64Histogram
	banSyncDuration          metric.Float64Histogram
	banSyncKeysTotal         metric.Int64Counter
	runtimeHeapAlloc         metric.Int64ObservableGauge
	runtimeHeapInuse         metric.Int64ObservableGauge
	runtimeHeapSys           metric.Int64ObservableGauge
	runtimeStackInuse        metric.Int64ObservableGauge
	runtimeStackSys          metric.Int64ObservableGauge
	runtimeSys               metric.Int64ObservableGauge
	runtimeTotalAlloc        metric.Int64ObservableGauge
	runtimeNumGC             metric.Int64ObservableGauge
	runtimeGoroutines        metric.Int64ObservableGauge
	runtimeCPUUsage          metric.Float64ObservableGauge
	runtimeCPUUserSeconds    metric.Float64ObservableGauge
	runtimeCPUSystemSeconds  metric.Float64ObservableGauge
	runtimeCPUTotalSeconds   metric.Float64ObservableGauge

	runtimeCPUSampleMu      sync.Mutex
	runtimeCPULastWall      time.Time
	runtimeCPULastTotalSecs float64
	runtimeCPULastUsagePct  float64
)

func InitMetrics(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	if !metricsEnabled() {
		return func(context.Context) error { return nil }, nil
	}

	resetRuntimeCPUSample()

	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}

	res, err := newResource(ctx, serviceName)
	if err != nil {
		return nil, err
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(15*time.Second))),
		sdkmetric.WithResource(res),
	)

	otel.SetMeterProvider(provider)
	meter = provider.Meter("pairline/go")

	if err := initInstruments(); err != nil {
		return nil, err
	}

	log.Printf("OpenTelemetry metrics enabled for %s", serviceName)
	return provider.Shutdown, nil
}

func initInstruments() error {
	var err error

	httpRequestsTotal, err = meter.Int64Counter("pairline.http.requests_total")
	if err != nil {
		return err
	}

	httpRequestDuration, err = meter.Float64Histogram("pairline.http.request.duration_ms", metric.WithUnit("ms"))
	if err != nil {
		return err
	}

	httpInflightRequests, err = meter.Int64UpDownCounter("pairline.http.requests_inflight")
	if err != nil {
		return err
	}

	businessEventsTotal, err = meter.Int64Counter("pairline.business.events_total")
	if err != nil {
		return err
	}

	webRTCActiveConnections, err = meter.Int64UpDownCounter("pairline.webrtc.connections_active")
	if err != nil {
		return err
	}

	webRTCConnectionDuration, err = meter.Float64Histogram("pairline.webrtc.connection.duration_ms", metric.WithUnit("ms"))
	if err != nil {
		return err
	}

	webRTCDisconnectsTotal, err = meter.Int64Counter("pairline.webrtc.disconnects_total")
	if err != nil {
		return err
	}

	turnRequestsTotal, err = meter.Int64Counter("pairline.webrtc.turn.requests_total")
	if err != nil {
		return err
	}

	turnRequestDuration, err = meter.Float64Histogram("pairline.webrtc.turn.duration_ms", metric.WithUnit("ms"))
	if err != nil {
		return err
	}

	banSyncDuration, err = meter.Float64Histogram("pairline.ban_sync.duration_ms", metric.WithUnit("ms"))
	if err != nil {
		return err
	}

	banSyncKeysTotal, err = meter.Int64Counter("pairline.ban_sync.keys_total")
	if err != nil {
		return err
	}

	runtimeHeapAlloc, err = meter.Int64ObservableGauge("pairline.runtime.memory.heap_alloc_bytes", metric.WithUnit("By"))
	if err != nil {
		return err
	}

	runtimeHeapInuse, err = meter.Int64ObservableGauge("pairline.runtime.memory.heap_inuse_bytes", metric.WithUnit("By"))
	if err != nil {
		return err
	}

	runtimeHeapSys, err = meter.Int64ObservableGauge("pairline.runtime.memory.heap_sys_bytes", metric.WithUnit("By"))
	if err != nil {
		return err
	}

	runtimeStackInuse, err = meter.Int64ObservableGauge("pairline.runtime.memory.stack_inuse_bytes", metric.WithUnit("By"))
	if err != nil {
		return err
	}

	runtimeStackSys, err = meter.Int64ObservableGauge("pairline.runtime.memory.stack_sys_bytes", metric.WithUnit("By"))
	if err != nil {
		return err
	}

	runtimeSys, err = meter.Int64ObservableGauge("pairline.runtime.memory.sys_bytes", metric.WithUnit("By"))
	if err != nil {
		return err
	}

	runtimeTotalAlloc, err = meter.Int64ObservableGauge("pairline.runtime.memory.total_alloc_bytes", metric.WithUnit("By"))
	if err != nil {
		return err
	}

	runtimeNumGC, err = meter.Int64ObservableGauge("pairline.runtime.gc.cycles")
	if err != nil {
		return err
	}

	runtimeGoroutines, err = meter.Int64ObservableGauge("pairline.runtime.goroutines")
	if err != nil {
		return err
	}

	runtimeCPUUsage, err = meter.Float64ObservableGauge("pairline.runtime.cpu.usage_percent")
	if err != nil {
		return err
	}

	runtimeCPUUserSeconds, err = meter.Float64ObservableGauge("pairline.runtime.cpu.user_seconds")
	if err != nil {
		return err
	}

	runtimeCPUSystemSeconds, err = meter.Float64ObservableGauge("pairline.runtime.cpu.system_seconds")
	if err != nil {
		return err
	}

	runtimeCPUTotalSeconds, err = meter.Float64ObservableGauge("pairline.runtime.cpu.total_seconds")
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		cpuUsagePct, cpuUserSeconds, cpuSystemSeconds, cpuTotalSeconds := sampleProcessCPU()

		observer.ObserveInt64(runtimeHeapAlloc, int64(mem.HeapAlloc))
		observer.ObserveInt64(runtimeHeapInuse, int64(mem.HeapInuse))
		observer.ObserveInt64(runtimeHeapSys, int64(mem.HeapSys))
		observer.ObserveInt64(runtimeStackInuse, int64(mem.StackInuse))
		observer.ObserveInt64(runtimeStackSys, int64(mem.StackSys))
		observer.ObserveInt64(runtimeSys, int64(mem.Sys))
		observer.ObserveInt64(runtimeTotalAlloc, int64(mem.TotalAlloc))
		observer.ObserveInt64(runtimeNumGC, int64(mem.NumGC))
		observer.ObserveInt64(runtimeGoroutines, int64(runtime.NumGoroutine()))
		observer.ObserveFloat64(runtimeCPUUsage, cpuUsagePct)
		observer.ObserveFloat64(runtimeCPUUserSeconds, cpuUserSeconds)
		observer.ObserveFloat64(runtimeCPUSystemSeconds, cpuSystemSeconds)
		observer.ObserveFloat64(runtimeCPUTotalSeconds, cpuTotalSeconds)
		return nil
	}, runtimeHeapAlloc, runtimeHeapInuse, runtimeHeapSys, runtimeStackInuse, runtimeStackSys, runtimeSys, runtimeTotalAlloc, runtimeNumGC, runtimeGoroutines, runtimeCPUUsage, runtimeCPUUserSeconds, runtimeCPUSystemSeconds, runtimeCPUTotalSeconds)
	return err
}

func metricsEnabled() bool {
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")) != ""
}

func RecordHTTPRequest(ctx context.Context, method, route string, status int, duration time.Duration) {
	attrs := metric.WithAttributes(
		attribute.String("http.method", method),
		attribute.String("http.route", route),
		attribute.Int("http.status_code", status),
	)
	httpRequestsTotal.Add(ctx, 1, attrs)
	httpRequestDuration.Record(ctx, durationMilliseconds(duration), attrs)
}

func AddHTTPInflight(ctx context.Context, delta int64, method, route string) {
	httpInflightRequests.Add(
		ctx,
		delta,
		metric.WithAttributes(
			attribute.String("http.method", method),
			attribute.String("http.route", route),
		),
	)
}

func RecordBusinessEvent(ctx context.Context, event string, attrs ...attribute.KeyValue) {
	allAttrs := append([]attribute.KeyValue{attribute.String("event.name", event)}, attrs...)
	businessEventsTotal.Add(ctx, 1, metric.WithAttributes(allAttrs...))
}

func AddWebRTCConnection(ctx context.Context, delta int64) {
	webRTCActiveConnections.Add(ctx, delta)
}

func RecordWebRTCConnectionClosed(ctx context.Context, duration time.Duration, reason string) {
	attrs := metric.WithAttributes(attribute.String("webrtc.disconnect.reason", reason))
	webRTCDisconnectsTotal.Add(ctx, 1, attrs)
	webRTCConnectionDuration.Record(ctx, durationMilliseconds(duration), attrs)
}

func RecordTURNRequest(ctx context.Context, duration time.Duration, outcome string, cacheHit bool) {
	attrs := metric.WithAttributes(
		attribute.String("turn.outcome", outcome),
		attribute.Bool("turn.cache_hit", cacheHit),
	)
	turnRequestsTotal.Add(ctx, 1, attrs)
	turnRequestDuration.Record(ctx, durationMilliseconds(duration), attrs)
}

func RecordBanSync(ctx context.Context, duration time.Duration, keys int) {
	banSyncDuration.Record(ctx, durationMilliseconds(duration))
	banSyncKeysTotal.Add(ctx, int64(keys))
}

func durationMilliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func sampleProcessCPU() (usagePercent, userSeconds, systemSeconds, totalSeconds float64) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, 0, 0, 0
	}

	userSeconds = timevalSeconds(usage.Utime)
	systemSeconds = timevalSeconds(usage.Stime)
	totalSeconds = userSeconds + systemSeconds
	now := time.Now()

	runtimeCPUSampleMu.Lock()
	defer runtimeCPUSampleMu.Unlock()

	if !runtimeCPULastWall.IsZero() {
		wallDelta := now.Sub(runtimeCPULastWall).Seconds()
		cpuDelta := totalSeconds - runtimeCPULastTotalSecs
		if wallDelta > 0 && cpuDelta >= 0 {
			runtimeCPULastUsagePct = (cpuDelta / wallDelta) * 100
		}
	}

	runtimeCPULastWall = now
	runtimeCPULastTotalSecs = totalSeconds
	return runtimeCPULastUsagePct, userSeconds, systemSeconds, totalSeconds
}

func resetRuntimeCPUSample() {
	runtimeCPUSampleMu.Lock()
	defer runtimeCPUSampleMu.Unlock()

	runtimeCPULastWall = time.Time{}
	runtimeCPULastTotalSecs = 0
	runtimeCPULastUsagePct = 0
}

func timevalSeconds(tv syscall.Timeval) float64 {
	return float64(tv.Sec) + float64(tv.Usec)/1_000_000
}
