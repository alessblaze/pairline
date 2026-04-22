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

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
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
	Cluster         RedisClusterInfo  `json:"cluster"`
	Nodes           []RedisNodeHealth `json:"nodes"`
}

type RedisClusterInfo struct {
	State                                string `json:"state"`
	SlotsAssigned                        int    `json:"slots_assigned"`
	SlotsOK                              int    `json:"slots_ok"`
	SlotsPFail                           int    `json:"slots_pfail"`
	SlotsFail                            int    `json:"slots_fail"`
	KnownNodes                           int    `json:"known_nodes"`
	Size                                 int    `json:"size"`
	CurrentEpoch                         int    `json:"current_epoch"`
	MyEpoch                              int    `json:"my_epoch"`
	TotalClusterLinksBufferLimitExceeded int    `json:"total_cluster_links_buffer_limit_exceeded"`
}

type RedisNodeHealth struct {
	NodeID                string             `json:"node_id"`
	Address               string             `json:"address"`
	Role                  string             `json:"role"`
	Status                string             `json:"status"`
	LinkState             string             `json:"link_state"`
	Flags                 []string           `json:"flags"`
	MasterID              string             `json:"master_id,omitempty"`
	Slots                 []string           `json:"slots,omitempty"`
	MasterLinkStatus      string             `json:"master_link_status,omitempty"`
	ReplicationLagSeconds int64              `json:"replication_lag_seconds,omitempty"`
	Memory                RedisMemoryInfo    `json:"memory"`
	CommandStats          []RedisCommandStat `json:"command_stats,omitempty"`
	Error                 string             `json:"error,omitempty"`
}

type RedisMemoryInfo struct {
	UsedMemoryBytes        int64   `json:"used_memory_bytes"`
	UsedMemoryHuman        string  `json:"used_memory_human"`
	UsedMemoryRSSBytes     int64   `json:"used_memory_rss_bytes"`
	UsedMemoryRSSHuman     string  `json:"used_memory_rss_human"`
	UsedMemoryPeakBytes    int64   `json:"used_memory_peak_bytes"`
	UsedMemoryPeakHuman    string  `json:"used_memory_peak_human"`
	UsedMemoryPeakPerc     string  `json:"used_memory_peak_perc"`
	UsedMemoryDatasetBytes int64   `json:"used_memory_dataset_bytes"`
	UsedMemoryDatasetPerc  string  `json:"used_memory_dataset_perc"`
	TotalSystemMemoryBytes int64   `json:"total_system_memory_bytes"`
	TotalSystemMemoryHuman string  `json:"total_system_memory_human"`
	MaxMemoryBytes         int64   `json:"maxmemory_bytes"`
	MaxMemoryHuman         string  `json:"maxmemory_human"`
	MaxMemoryPolicy        string  `json:"maxmemory_policy"`
	Allocator              string  `json:"allocator"`
	FragmentationRatio     float64 `json:"fragmentation_ratio"`
	FragmentationBytes     int64   `json:"fragmentation_bytes"`
}

type RedisCommandStat struct {
	Command     string  `json:"command"`
	Calls       int64   `json:"calls"`
	UsecTotal   int64   `json:"usec_total"`
	UsecPerCall float64 `json:"usec_per_call"`
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
	pingErr := redisClient.GetClient().Ping(ctx).Err()
	result.LatencyMS = durationMillisCeil(time.Since(startedAt))
	if pingErr != nil {
		result.Status = "error"
		result.Error = pingErr.Error()
	}

	clusterClient, ok := redisClient.GetClient().(*goredis.ClusterClient)
	if !ok {
		return result
	}

	clusterInfoRaw, err := clusterClient.ClusterInfo(ctx).Result()
	if err != nil {
		if result.Error == "" {
			result.Status = "error"
			result.Error = err.Error()
		}
		return result
	}

	clusterInfo := parseRedisClusterInfo(clusterInfoRaw)
	clusterState := strings.ToLower(clusterInfo["cluster_state"])
	slotsAssigned := redisClusterInfoInt(clusterInfo, "cluster_slots_assigned")
	slotsOK := redisClusterInfoInt(clusterInfo, "cluster_slots_ok")
	slotsPFail := redisClusterInfoInt(clusterInfo, "cluster_slots_pfail")
	slotsFail := redisClusterInfoInt(clusterInfo, "cluster_slots_fail")
	result.Cluster = RedisClusterInfo{
		State:                                clusterState,
		SlotsAssigned:                        slotsAssigned,
		SlotsOK:                              slotsOK,
		SlotsPFail:                           slotsPFail,
		SlotsFail:                            slotsFail,
		KnownNodes:                           redisClusterInfoInt(clusterInfo, "cluster_known_nodes"),
		Size:                                 redisClusterInfoInt(clusterInfo, "cluster_size"),
		CurrentEpoch:                         redisClusterInfoInt(clusterInfo, "cluster_current_epoch"),
		MyEpoch:                              redisClusterInfoInt(clusterInfo, "cluster_my_epoch"),
		TotalClusterLinksBufferLimitExceeded: redisClusterInfoInt(clusterInfo, "total_cluster_links_buffer_limit_exceeded"),
	}

	switch {
	case clusterState != "" && clusterState != "ok":
		result.Status = "error"
		if result.Error == "" {
			result.Error = "cluster_state=" + clusterState
		}
	case slotsFail > 0:
		result.Status = "error"
		if result.Error == "" {
			result.Error = "cluster reports failing slots"
		}
	case slotsAssigned > 0 && slotsAssigned < 16384:
		result.Status = "error"
		if result.Error == "" {
			result.Error = "cluster has unassigned slots"
		}
	case slotsPFail > 0 || (slotsAssigned > 0 && slotsOK > 0 && slotsOK < slotsAssigned):
		if result.Status == "ok" {
			result.Status = "degraded"
		}
		if result.Error == "" {
			result.Error = "cluster reports pfail or reduced slot coverage"
		}
	case pingErr != nil:
		result.Status = "degraded"
	}

	clusterNodesRaw, err := clusterClient.ClusterNodes(ctx).Result()
	if err != nil {
		if result.Status == "ok" {
			result.Status = "degraded"
		}
		if result.Error == "" {
			result.Error = "cluster nodes unavailable: " + err.Error()
		}
		return result
	}
	result.Nodes = parseRedisClusterNodes(clusterNodesRaw)
	enrichRedisNodeDiagnostics(ctx, &result)
	for _, node := range result.Nodes {
		if node.Status != "ok" && result.Status == "ok" {
			result.Status = "degraded"
			break
		}
	}

	return result
}

func checkObservabilityHealth(ctx context.Context) ObservabilityHealth {
	tracesConfigured := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) != ""
	metricsConfigured := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")) != ""
	telemetryConfigured := tracesConfigured || metricsConfigured

	collectorURL := strings.TrimSpace(os.Getenv("OTEL_COLLECTOR_HEALTH_URL"))
	collector := CollectorHealth{
		URL:    collectorURL,
		Status: "ok",
	}
	if collectorURL != "" {
		collector = pingCollectorHealthURL(ctx, collectorURL)
	} else if telemetryConfigured {
		collector.Status = "degraded"
		collector.Error = "collector health URL not configured"
	}
	status := "ok"
	if telemetryConfigured && collector.Status != "ok" {
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
	goTargets = append(goTargets, parseURLListEnv("ADMIN_HEALTH_TURN_URLS", nil)...)
	if len(goTargets) == 0 {
		targets = append(targets, probeTarget{
			name:   "ADMIN_HEALTH_GO_URLS,ADMIN_HEALTH_TURN_URLS",
			kind:   "config",
			rawURL: "env://ADMIN_HEALTH_GO_URLS,ADMIN_HEALTH_TURN_URLS",
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
	if len(redisHealth.Nodes) == 0 && redisHealth.Status == "ok" {
		reachableRedis = len(redisHealth.ConfiguredNodes)
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

func parseRedisClusterInfo(raw string) map[string]string {
	info := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		info[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return info
}

func redisClusterInfoInt(info map[string]string, key string) int {
	value := strings.TrimSpace(info[key])
	if value == "" {
		return 0
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func redisInfoInt64(info map[string]string, key string) int64 {
	value := strings.TrimSpace(info[key])
	if value == "" {
		return 0
	}

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func redisInfoFloat64(info map[string]string, key string) float64 {
	value := strings.TrimSpace(strings.TrimSuffix(info[key], "%"))
	if value == "" {
		return 0
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func parseRedisClusterNodes(raw string) []RedisNodeHealth {
	nodes := make([]RedisNodeHealth, 0)

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}

		flags := splitRedisClusterFlags(fields[2])
		masterID := strings.TrimSpace(fields[3])
		if masterID == "-" {
			masterID = ""
		}

		node := RedisNodeHealth{
			NodeID:    fields[0],
			Address:   parseRedisClusterNodeAddress(fields[1]),
			Role:      inferRedisClusterNodeRole(flags),
			Status:    inferRedisClusterNodeStatus(flags, fields[7]),
			LinkState: fields[7],
			Flags:     flags,
			MasterID:  masterID,
			Slots:     append([]string(nil), fields[8:]...),
		}
		nodes = append(nodes, node)
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Address < nodes[j].Address
	})

	return nodes
}

func enrichRedisNodeDiagnostics(ctx context.Context, health *RedisHealth) {
	if health == nil || len(health.Nodes) == 0 {
		return
	}

	password := strings.TrimSpace(os.Getenv("REDIS_PASSWORD"))

	for i := range health.Nodes {
		node := &health.Nodes[i]
		if strings.TrimSpace(node.Address) == "" {
			continue
		}

		client := goredis.NewClient(&goredis.Options{
			Addr:         node.Address,
			Password:     password,
			DialTimeout:  750 * time.Millisecond,
			ReadTimeout:  time.Second,
			WriteTimeout: time.Second,
		})

		enrichSingleRedisNode(ctx, client, node)
		_ = client.Close()

		if node.Status == "ok" && node.Error != "" {
			node.Status = "degraded"
		}
	}
}

func enrichSingleRedisNode(ctx context.Context, client *goredis.Client, node *RedisNodeHealth) {
	memoryRaw, err := client.Info(ctx, "memory").Result()
	if err != nil {
		node.Error = appendError(node.Error, "memory info unavailable: "+err.Error())
	} else {
		memoryInfo := parseRedisClusterInfo(memoryRaw)
		node.Memory = RedisMemoryInfo{
			UsedMemoryBytes:        redisInfoInt64(memoryInfo, "used_memory"),
			UsedMemoryHuman:        memoryInfo["used_memory_human"],
			UsedMemoryRSSBytes:     redisInfoInt64(memoryInfo, "used_memory_rss"),
			UsedMemoryRSSHuman:     memoryInfo["used_memory_rss_human"],
			UsedMemoryPeakBytes:    redisInfoInt64(memoryInfo, "used_memory_peak"),
			UsedMemoryPeakHuman:    memoryInfo["used_memory_peak_human"],
			UsedMemoryPeakPerc:     memoryInfo["used_memory_peak_perc"],
			UsedMemoryDatasetBytes: redisInfoInt64(memoryInfo, "used_memory_dataset"),
			UsedMemoryDatasetPerc:  memoryInfo["used_memory_dataset_perc"],
			TotalSystemMemoryBytes: redisInfoInt64(memoryInfo, "total_system_memory"),
			TotalSystemMemoryHuman: memoryInfo["total_system_memory_human"],
			MaxMemoryBytes:         redisInfoInt64(memoryInfo, "maxmemory"),
			MaxMemoryHuman:         memoryInfo["maxmemory_human"],
			MaxMemoryPolicy:        memoryInfo["maxmemory_policy"],
			Allocator:              memoryInfo["mem_allocator"],
			FragmentationRatio:     redisInfoFloat64(memoryInfo, "mem_fragmentation_ratio"),
			FragmentationBytes:     redisInfoInt64(memoryInfo, "mem_fragmentation_bytes"),
		}
	}

	replicationRaw, err := client.Info(ctx, "replication").Result()
	if err != nil {
		node.Error = appendError(node.Error, "replication info unavailable: "+err.Error())
	} else {
		replicationInfo := parseRedisClusterInfo(replicationRaw)
		node.MasterLinkStatus = replicationInfo["master_link_status"]
		node.ReplicationLagSeconds = redisInfoInt64(replicationInfo, "master_last_io_seconds_ago")
	}

	commandStatsRaw, err := client.Info(ctx, "commandstats").Result()
	if err != nil {
		node.Error = appendError(node.Error, "commandstats unavailable: "+err.Error())
	} else {
		node.CommandStats = parseRedisCommandStats(commandStatsRaw)
	}
}

func parseRedisCommandStats(raw string) []RedisCommandStat {
	stats := make([]RedisCommandStat, 0)
	info := parseRedisClusterInfo(raw)

	for key, value := range info {
		if !strings.HasPrefix(key, "cmdstat_") {
			continue
		}

		command := strings.TrimPrefix(key, "cmdstat_")
		metrics := parseRedisInfoCSV(value)
		stats = append(stats, RedisCommandStat{
			Command:     command,
			Calls:       redisInfoInt64(metrics, "calls"),
			UsecTotal:   redisInfoInt64(metrics, "usec"),
			UsecPerCall: redisInfoFloat64(metrics, "usec_per_call"),
		})
	}

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Calls == stats[j].Calls {
			return stats[i].Command < stats[j].Command
		}
		return stats[i].Calls > stats[j].Calls
	})

	return stats
}

func parseRedisInfoCSV(raw string) map[string]string {
	values := make(map[string]string)

	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	return values
}

func splitRedisClusterFlags(raw string) []string {
	parts := strings.Split(raw, ",")
	flags := make([]string, 0, len(parts))
	for _, part := range parts {
		flag := strings.TrimSpace(part)
		if flag != "" {
			flags = append(flags, flag)
		}
	}
	return flags
}

func parseRedisClusterNodeAddress(raw string) string {
	address := raw
	if trimmed, _, ok := strings.Cut(address, "@"); ok {
		address = trimmed
	}
	if trimmed, _, ok := strings.Cut(address, ","); ok {
		address = trimmed
	}
	return strings.TrimSpace(address)
}

func inferRedisClusterNodeRole(flags []string) string {
	switch {
	case containsString(flags, "master"):
		return "master"
	case containsString(flags, "slave"), containsString(flags, "replica"):
		return "replica"
	default:
		return "unknown"
	}
}

func inferRedisClusterNodeStatus(flags []string, linkState string) string {
	switch {
	case containsString(flags, "fail"), containsString(flags, "noaddr"):
		return "error"
	case containsString(flags, "fail?"), containsString(flags, "pfail"), containsString(flags, "handshake"):
		return "degraded"
	case strings.TrimSpace(strings.ToLower(linkState)) != "connected":
		return "degraded"
	default:
		return "ok"
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendError(existing, next string) string {
	if strings.TrimSpace(existing) == "" {
		return next
	}
	if strings.TrimSpace(next) == "" {
		return existing
	}
	return existing + "; " + next
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
