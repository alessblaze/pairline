package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/storage"
	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/codes"
)

type InfraHealthResponse struct {
	Status        string                `json:"status"`
	Service       string                `json:"service"`
	Timestamp     int64                 `json:"timestamp"`
	Topology      InfraTopology         `json:"topology"`
	Postgres      PostgresHealth        `json:"postgres"`
	Redis         RedisHealth           `json:"redis"`
	Observability ObservabilityHealth   `json:"observability"`
	Services      []RemoteServiceHealth `json:"services"`
	Summary       InfraSummary          `json:"summary"`
}

type InfraTopology struct {
	PhoenixConfiguredNodes int      `json:"phoenix_configured_nodes"`
	PhoenixConnectedNodes  int      `json:"phoenix_connected_nodes"`
	PhoenixNodeNames       []string `json:"phoenix_node_names"`
	GoConfiguredServices   int      `json:"go_configured_services"`
	RedisConfiguredNodes   int      `json:"redis_configured_nodes"`
	RedisReachableNodes    int      `json:"redis_reachable_nodes"`
}

type InfraSummary struct {
	HealthyServices  int `json:"healthy_services"`
	DegradedServices int `json:"degraded_services"`
	TotalServices    int `json:"total_services"`
}

type PostgresHealth struct {
	Status      string        `json:"status"`
	LatencyMS   int64         `json:"latency_ms"`
	Error       string        `json:"error,omitempty"`
	Connections PostgresStats `json:"connections"`
}

type PostgresStats struct {
	Open    int `json:"open"`
	InUse   int `json:"in_use"`
	Idle    int `json:"idle"`
	MaxOpen int `json:"max_open"`
}

type RedisHealth struct {
	Status          string            `json:"status"`
	LatencyMS       int64             `json:"latency_ms"`
	Error           string            `json:"error,omitempty"`
	ConfiguredNodes []string          `json:"configured_nodes"`
	Nodes           []RedisNodeHealth `json:"nodes"`
}

type RedisNodeHealth struct {
	Address   string `json:"address"`
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

type ObservabilityHealth struct {
	Status            string          `json:"status"`
	TracesConfigured  bool            `json:"traces_configured"`
	MetricsConfigured bool            `json:"metrics_configured"`
	OTLPEndpoint      string          `json:"otlp_endpoint"`
	Collector         CollectorHealth `json:"collector"`
}

type CollectorHealth struct {
	URL       string `json:"url"`
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

type RemoteServiceHealth struct {
	Name       string                 `json:"name"`
	Kind       string                 `json:"kind"`
	URL        string                 `json:"url"`
	Status     string                 `json:"status"`
	HTTPStatus int                    `json:"http_status"`
	LatencyMS  int64                  `json:"latency_ms"`
	Error      string                 `json:"error,omitempty"`
	Service    string                 `json:"service,omitempty"`
	ReportedAt int64                  `json:"reported_at,omitempty"`
	Details    map[string]interface{} `json:"details,omitempty"`
}

var infraHTTPClient = &http.Client{Timeout: 3 * time.Second}

func InfraHealthHandlerGin(redisClient *appredis.Client, db *storage.Database, serviceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		span := startHandlerSpan(c, "admin.infra.health")
		defer span.End()

		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()

		postgresHealth := checkPostgresHealth(ctx, db)
		redisHealth := checkRedisHealth(ctx, redisClient)
		observabilityHealth := checkObservabilityHealth(ctx)
		remoteServices := fetchConfiguredServiceHealth(ctx)

		topology := buildInfraTopology(remoteServices, redisHealth)
		summary := summarizeInfraHealth(postgresHealth, redisHealth, observabilityHealth, remoteServices)

		status := "ok"
		if summary.DegradedServices > 0 {
			status = "degraded"
		}

		if status != "ok" {
			span.SetStatus(codes.Error, "infrastructure degraded")
		}

		c.JSON(http.StatusOK, InfraHealthResponse{
			Status:        status,
			Service:       serviceName,
			Timestamp:     time.Now().UnixMilli(),
			Topology:      topology,
			Postgres:      postgresHealth,
			Redis:         redisHealth,
			Observability: observabilityHealth,
			Services:      remoteServices,
			Summary:       summary,
		})
	}
}

func checkPostgresHealth(ctx context.Context, db *storage.Database) PostgresHealth {
	result := PostgresHealth{
		Status:      "ok",
		Connections: PostgresStats{},
	}

	if db == nil || db.GetDB() == nil {
		result.Status = "error"
		result.Error = "database unavailable"
		return result
	}

	sqlDB, err := db.GetDB().DB()
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		return result
	}

	startedAt := time.Now()
	if err := sqlDB.PingContext(ctx); err != nil {
		result.Status = "error"
		result.Error = err.Error()
		result.LatencyMS = durationMillisCeil(time.Since(startedAt))
		return result
	}

	stats := sqlDB.Stats()
	result.LatencyMS = durationMillisCeil(time.Since(startedAt))
	result.Connections = PostgresStats{
		Open:    stats.OpenConnections,
		InUse:   stats.InUse,
		Idle:    stats.Idle,
		MaxOpen: stats.MaxOpenConnections,
	}
	return result
}

func checkRedisHealth(ctx context.Context, redisClient *appredis.Client) RedisHealth {
	result := RedisHealth{
		Status:          "ok",
		ConfiguredNodes: redisClusterNodesFromEnv(),
		Nodes:           []RedisNodeHealth{},
	}

	if redisClient == nil || redisClient.GetClient() == nil {
		result.Status = "error"
		result.Error = "redis unavailable"
		return result
	}

	startedAt := time.Now()
	if err := redisClient.GetClient().Ping(ctx).Err(); err != nil {
		result.Status = "error"
		result.Error = err.Error()
		result.LatencyMS = durationMillisCeil(time.Since(startedAt))
		return result
	}
	result.LatencyMS = durationMillisCeil(time.Since(startedAt))

	clusterClient, ok := redisClient.GetClient().(*goredis.ClusterClient)
	if !ok {
		return result
	}

	seen := map[string]struct{}{}
	nodeStatuses := make([]RedisNodeHealth, 0)

	_ = clusterClient.ForEachShard(ctx, func(shardCtx context.Context, shard *goredis.Client) error {
		addr := shard.Options().Addr
		if _, exists := seen[addr]; exists {
			return nil
		}
		seen[addr] = struct{}{}

		nodeStartedAt := time.Now()
		pingErr := shard.Ping(shardCtx).Err()
		nodeStatus := RedisNodeHealth{
			Address:   addr,
			LatencyMS: durationMillisCeil(time.Since(nodeStartedAt)),
		}
		if pingErr != nil {
			nodeStatus.Status = "error"
			nodeStatus.Error = pingErr.Error()
			result.Status = "degraded"
		} else {
			nodeStatus.Status = "ok"
		}

		nodeStatuses = append(nodeStatuses, nodeStatus)
		return nil
	})

	sort.Slice(nodeStatuses, func(i, j int) bool {
		return nodeStatuses[i].Address < nodeStatuses[j].Address
	})
	result.Nodes = nodeStatuses
	return result
}

func checkObservabilityHealth(ctx context.Context) ObservabilityHealth {
	tracesConfigured := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) != ""
	metricsConfigured := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")) != ""

	collectorURL := strings.TrimSpace(os.Getenv("OTEL_COLLECTOR_HEALTH_URL"))
	collector := CollectorHealth{Status: "degraded"}
	if collectorURL != "" {
		collector = pingCollectorHealthURL(ctx, collectorURL)
	} else {
		collector.Error = "collector health URL not configured"
	}
	status := "ok"
	if (!tracesConfigured && !metricsConfigured) || collector.Status != "ok" {
		status = "degraded"
	}

	return ObservabilityHealth{
		Status:            status,
		TracesConfigured:  tracesConfigured,
		MetricsConfigured: metricsConfigured,
		OTLPEndpoint:      firstNonEmpty(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"), os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")),
		Collector: CollectorHealth{
			URL:       collector.URL,
			Status:    collector.Status,
			LatencyMS: collector.LatencyMS,
			Error:     collector.Error,
		},
	}
}

func pingCollectorHealthURL(ctx context.Context, rawURL string) CollectorHealth {
	result := CollectorHealth{
		URL:    rawURL,
		Status: "error",
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	startedAt := time.Now()
	resp, err := infraHTTPClient.Do(req)
	if err != nil {
		result.Error = err.Error()
		result.LatencyMS = durationMillisCeil(time.Since(startedAt))
		return result
	}
	defer resp.Body.Close()

	result.LatencyMS = durationMillisCeil(time.Since(startedAt))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Status = "ok"
		return result
	}

	result.Status = "degraded"
	result.Error = resp.Status
	return result
}

func fetchConfiguredServiceHealth(ctx context.Context) []RemoteServiceHealth {
	sharedSecret := strings.TrimSpace(os.Getenv("SHARED_SECRET"))
	headers := map[string]string{}
	if sharedSecret != "" {
		headers["x-shared-secret"] = sharedSecret
	}

	type probeTarget struct {
		name    string
		kind    string
		rawURL  string
		headers map[string]string
	}

	targets := make([]probeTarget, 0)
	phoenixTargets := parseURLListEnv("ADMIN_HEALTH_PHOENIX_URLS", nil)
	if len(phoenixTargets) == 0 {
		targets = append(targets, probeTarget{
			name:   "ADMIN_HEALTH_PHOENIX_URLS",
			kind:   "config",
			rawURL: "env://ADMIN_HEALTH_PHOENIX_URLS",
		})
	}
	for _, targetURL := range phoenixTargets {
		targets = append(targets, probeTarget{
			name:    inferServiceName(targetURL),
			kind:    "phoenix",
			rawURL:  targetURL,
			headers: headers,
		})
	}

	goTargets := parseURLListEnv("ADMIN_HEALTH_GO_URLS", nil)
	if len(goTargets) == 0 {
		targets = append(targets, probeTarget{
			name:   "ADMIN_HEALTH_GO_URLS",
			kind:   "config",
			rawURL: "env://ADMIN_HEALTH_GO_URLS",
		})
	}
	for _, targetURL := range goTargets {
		targets = append(targets, probeTarget{
			name:   inferServiceName(targetURL),
			kind:   "go",
			rawURL: targetURL,
		})
	}

	services := make([]RemoteServiceHealth, len(targets))
	var wg sync.WaitGroup
	wg.Add(len(targets))

	for idx, target := range targets {
		go func(index int, target probeTarget) {
			defer wg.Done()
			if target.kind == "config" {
				services[index] = RemoteServiceHealth{
					Name:   target.name,
					Kind:   target.kind,
					URL:    target.rawURL,
					Status: "degraded",
					Error:  target.name + " not configured",
				}
				return
			}
			services[index] = pingHealthURL(ctx, target.name, target.kind, target.rawURL, target.headers)
		}(idx, target)
	}

	wg.Wait()

	sort.Slice(services, func(i, j int) bool {
		if services[i].Kind == services[j].Kind {
			return services[i].Name < services[j].Name
		}
		return services[i].Kind < services[j].Kind
	})

	return services
}

func pingHealthURL(ctx context.Context, name, kind, rawURL string, headers map[string]string) RemoteServiceHealth {
	result := RemoteServiceHealth{
		Name:   name,
		Kind:   kind,
		URL:    rawURL,
		Status: "error",
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}

	startedAt := time.Now()
	resp, err := infraHTTPClient.Do(req)
	if err != nil {
		result.Error = err.Error()
		result.LatencyMS = durationMillisCeil(time.Since(startedAt))
		return result
	}
	defer resp.Body.Close()

	result.HTTPStatus = resp.StatusCode
	result.LatencyMS = durationMillisCeil(time.Since(startedAt))

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		result.Error = err.Error()
		return result
	}

	result.Details = payload
	result.Service = stringValue(payload["service"])
	result.ReportedAt = int64Value(payload["timestamp"])

	if resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.EqualFold(stringValue(payload["status"]), "ok") {
		result.Status = "ok"
	} else {
		result.Status = "degraded"
		if result.Error == "" {
			result.Error = stringValue(payload["error"])
		}
	}

	return result
}

func durationMillisCeil(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}

	ms := d.Milliseconds()
	if ms == 0 {
		return 1
	}

	return ms
}

func buildInfraTopology(services []RemoteServiceHealth, redisHealth RedisHealth) InfraTopology {
	phoenixConfigured := 0
	goConfigured := 0
	phoenixNodes := map[string]struct{}{}
	maxConnected := 0

	for _, service := range services {
		switch service.Kind {
		case "phoenix":
			phoenixConfigured++
			if nodeName := stringValue(service.Details["node"]); nodeName != "" {
				phoenixNodes[nodeName] = struct{}{}
			}
			for _, nodeName := range stringSlice(service.Details["connected_nodes"]) {
				if nodeName != "" {
					phoenixNodes[nodeName] = struct{}{}
				}
			}
			connectedCount := len(stringSlice(service.Details["connected_nodes"]))
			if connectedCount+1 > maxConnected {
				maxConnected = connectedCount + 1
			}
		case "go":
			goConfigured++
		}
	}

	nodeNames := make([]string, 0, len(phoenixNodes))
	for nodeName := range phoenixNodes {
		nodeNames = append(nodeNames, nodeName)
	}
	sort.Strings(nodeNames)

	reachableRedis := 0
	for _, node := range redisHealth.Nodes {
		if node.Status == "ok" {
			reachableRedis++
		}
	}

	return InfraTopology{
		PhoenixConfiguredNodes: phoenixConfigured,
		PhoenixConnectedNodes:  maxConnected,
		PhoenixNodeNames:       nodeNames,
		GoConfiguredServices:   goConfigured,
		RedisConfiguredNodes:   len(redisHealth.ConfiguredNodes),
		RedisReachableNodes:    reachableRedis,
	}
}

func summarizeInfraHealth(postgres PostgresHealth, redis RedisHealth, observability ObservabilityHealth, services []RemoteServiceHealth) InfraSummary {
	healthy := 0
	degraded := 0

	countStatus := func(status string) {
		if status == "ok" {
			healthy++
		} else {
			degraded++
		}
	}

	countStatus(postgres.Status)
	countStatus(redis.Status)
	countStatus(observability.Status)
	for _, service := range services {
		countStatus(service.Status)
	}

	return InfraSummary{
		HealthyServices:  healthy,
		DegradedServices: degraded,
		TotalServices:    healthy + degraded,
	}
}

func parseURLListEnv(key string, fallback []string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}

	values := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}

	if len(values) == 0 {
		return fallback
	}
	return values
}

func redisClusterNodesFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("REDIS_CLUSTER_NODES"))
	if raw == "" {
		return nil
	}

	nodes := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			nodes = append(nodes, trimmed)
		}
	}
	return nodes
}

func inferServiceName(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := parsed.Hostname()
	if host == "" {
		return rawURL
	}
	return host
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func int64Value(value interface{}) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	default:
		return 0
	}
}

func stringSlice(value interface{}) []string {
	raw, ok := value.([]interface{})
	if !ok {
		return nil
	}

	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			result = append(result, s)
		}
	}
	return result
}
