# Environment variables reference

This repo uses environment variables heavily across services. The authoritative starting points are:

- Phoenix: `backend/elixir/omegle_phoenix/.env.example`
- Go: `backend/golang/.env.example`
- Frontend client/admin: `frontend/*/.env.example`
- Local cluster (dev only): `docker/docker-compose.yml`

Below is a consolidated reference for the **backend** services (Phoenix + Go).

- **default**: what happens if the variable is unset
- **required**: the service will refuse to boot (or key routes won’t work) if missing
- **format**: expected shape of the value

This file focuses on **what each variable changes in runtime behavior**, not just what it is called.

## Quick pitfalls (common misconfigurations)
- **`SHARED_SECRET` mismatch**: Phoenix and Go must share the same value or cross-service auth/session operations will fail.
- **CORS naming differs**: Phoenix uses **`CORS_ORIGINS`**, Go uses **`CORS_ORIGIN`** (both are comma-separated lists).
- **Redis cluster seed list**: `REDIS_CLUSTER_NODES`/`REDIS_CLUSTER_NODES` is a comma-separated `host:port` list (no protocol).
- **Proxy IP trust**: if `TRUSTED_PROXY_CIDRS` is wrong, you’ll log/ban the proxy IP instead of the real client IP.

---

## Phoenix backend (`backend/elixir/omegle_phoenix`)

### Network / endpoint
- **`PORT`** (default: `8080`): HTTP port Phoenix listens on.
- **`PHX_HOST`** (default: `localhost` in dev, `example.com` in prod config): public hostname used when Phoenix builds absolute URLs (health, redirects, etc.).
- **`ENABLE_IPV6`** (default: `false`; format: `true|false`): when `true`, binds on IPv6 `::` (dual-stack) instead of IPv4-only `0.0.0.0`.

### Secrets / security
- **`SECRET_KEY_BASE`** (**required**): encrypts/signs Phoenix secrets (sessions, cookies, etc.). Changing it invalidates existing signed/encrypted values.
- **`SHARED_SECRET`** (**required**): shared secret used for cross-service trust (must match Go’s `SHARED_SECRET`). Treat as a long random value.

### CORS / client origins
- **`CORS_ORIGINS`** (default varies by env; format: comma-separated origins): controls Phoenix `check_origin` for the websocket and HTTP origin checks.
  - Include your browser app origins (e.g. `http://localhost:5173`).
  - If you see “WebSocket rejected origin” logs, this list is missing the requesting origin.

### Reverse proxy / client IP handling
- **`TRUSTED_PROXY_CIDRS`** (default varies; format: comma-separated CIDRs): which proxy hops are trusted to supply `X-Forwarded-For` / `X-Real-IP`.
  - Set this correctly if you run behind Nginx, a tunnel, or a CDN; otherwise bans/moderation can target the wrong IP.

### Redis / Valkey
- **`REDIS_HOST`** / **`REDIS_PORT`** (defaults: `localhost` / `7000`): single-node address (kept for compatibility; the app expects cluster mode in most deployments).
- **`REDIS_CLUSTER_NODES`** (format: comma-separated `host:port`): seed nodes for cluster discovery (the client will learn the full cluster topology from these).
- **`REDIS_PASSWORD`** (default: empty): password for Redis/Valkey auth (must match `requirepass` on the Redis nodes).
- **`REDIS_POOL_SIZE`** (default: `256`): connection pool size. Increase if Redis latency rises under high concurrency and you see pool exhaustion; too high can overwhelm Redis with connections.
- **`FINCH_POOL_SIZE`** (default: `10`): outbound Finch HTTP connection pool size used for Turnstile and any future Phoenix HTTP client traffic.

### Session lifecycle
- **`SESSION_TTL`** (default: `3600`): how long session records stay alive in Redis after last refresh. Larger values reduce accidental session expiry but increase orphan memory.
- **`REPORT_GRACE_SECONDS`** (default: `900`): how long after disconnect a user can still report the last peer (used by moderation/report flows).

### Matchmaking (queue + streams coordination)
- **`MATCH_TIMEOUT`** (default: `30000` ms): how long a search can run before timing out.
- **`MATCH_LEADER_TTL_MS`** (default: `5000`): lease TTL for “matchmaker leader” work to avoid multiple nodes doing identical heavy passes.
- **`MATCH_BATCH_SIZE`** (default: `500`): how many queued session IDs are pulled at a time from a queue head.
- **`MATCH_FRONTIER_SIZE`** (default: `40`): caps how deep into the head of each interest bucket the matcher will look in one pass.
  - Higher increases match quality/fairness under load but increases Redis reads.
- **`MATCH_SHARD_COUNT`** (default: `12`): number of relaxed/random shards to fan out across (more shards reduces hot-spotting but increases coordination overhead).
- **`MATCH_RELAXED_WAIT_MS`** (default: `5000`): when a user can “relax” from exact interest matching into a broader relaxed tier.
- **`MATCH_OVERFLOW_WAIT_MS`** (default: `15000`): when a user can fall back to “random” matching.
- **`MATCH_EVENT_STREAM`** / **`MATCH_EVENT_STREAM_GROUP`**: stream + consumer group used for cross-node coordination (ensures nodes don’t all sweep the same shards).
- **`MATCH_EVENT_STREAM_BLOCK_MS`** / **`MATCH_EVENT_STREAM_BATCH_SIZE`**: polling behavior (lower block = more responsive but more Redis calls; larger batch = fewer calls but more bursty processing).
  - `MATCH_EVENT_STREAM_BLOCK_MS` defaults to `1000` and is capped at `4000` in code to stay below the underlying Redis client timeout.
- **`MATCH_EVENT_STREAM_MAXLEN`**: caps stream size to bound memory (too low can drop coordination history aggressively; too high increases memory).
- **`MATCH_SWEEP_INTERVAL_MS`** / **`MATCH_SWEEP_STALE_AFTER_MS`**: safety sweep knobs for “quiet” queues so they don’t get stuck if stream-triggered events are missed.

### Reaper (orphan cleanup)
- **`REAPER_INTERVAL_MS`** (default: `10000`): how often the reaper scans for stale/orphaned sessions/queue entries.
- **`REAPER_BATCH_SIZE`** (default: `200`): work chunk size per pass. Increase for faster cleanup at the cost of more Redis load.

### Admin moderation coordination (durable stream)
- **`ADMIN_STREAM`** (default: `admin:action:stream`): stream name used to fan out admin “emergency” actions across Phoenix nodes (durable coordination).
- **`ADMIN_STREAM_GROUP`** (default: `admin:workers`): consumer group name for Phoenix nodes.
- **`ADMIN_STREAM_BLOCK_MS`** (default: `1000`): how long consumers block waiting for new admin actions.
  - This value is capped at `4000` in code to stay below the underlying Redis client timeout.
- **`ADMIN_STREAM_BATCH_SIZE`** (default: `50`): max actions processed per poll.

### BEAM clustering (optional)
- **`NODE_NAME`** (default: empty): Erlang node name. Required if you want Phoenix nodes to connect to each other (multi-node cluster).
- **`NODE_COOKIE`** (default: empty): shared Erlang cookie. Must match across Phoenix nodes or clustering will fail silently.
- **`NODE_DISTRIBUTION`** (default: `short`): `short` for short hostnames (common in Docker), `long` for FQDN-style names.
- **`CLUSTER_NODES`** (default: empty): comma-separated list of node names to connect to (must include all nodes you expect to join).
- **`CLUSTER_INITIAL_CONNECT_DELAY_MS`** (default: `3000`)
- **`CLUSTER_CONNECT_INTERVAL_MS`** (default: `5000`)
- **`CLUSTER_CONNECT_RETRY_ATTEMPTS`** (default: `3`)
- **`CLUSTER_CONNECT_RETRY_DELAY_MS`** (default: `1000`)
- **`ROUTER_OWNER_TTL_SECONDS`** (default: `30`): TTL for per-session “owner” records used for routing signals to the right Phoenix node.
- **`STREAM_STALE_CONSUMER_IDLE_MS`** (default: `300000`): how long an idle stream consumer can sit before being considered stale (used for cleanup/rebalancing).

### Turnstile (captcha)
- **`TURNSTILE_SECRET_KEY`** (default in dev example is Cloudflare test key): server-side secret used to verify Turnstile tokens from the frontend.
- **`TURNSTILE_ALLOW_INSECURE_BYPASS`** (default: `false`): when `true`, allows a “fail open” mode if Turnstile is misconfigured. Keep `false` in production.

### Health endpoint
- **`HEALTH_DETAILS_ENABLED`** (default: `false`): when enabled, `/api/health` includes node identity and internal counters. Keep disabled on public deployments unless protected.

### OpenTelemetry tracing
- **`OTEL_EXPORTER_OTLP_ENDPOINT`** (default: unset): base OTLP endpoint for traces and metrics, for example `http://otel-collector:4318`. When unset, spans can still be created locally but are not exported anywhere useful.
- **`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`** (default: unset): trace-specific OTLP HTTP endpoint, useful when your collector requires an explicit `/v1/traces` path.
- **`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`** (default: unset): metric-specific OTLP HTTP endpoint, useful when your collector requires an explicit `/v1/metrics` path.
- **`OTEL_EXPORTER_OTLP_INSECURE`** (default: exporter default; common dev value: `true`): use plaintext OTLP over HTTP for local collectors.
- **`OTEL_ENVIRONMENT`** (default: `development`): exported as the deployment environment on spans.
- **`OTEL_SERVICE_NAME`** (default: exporter/library default): explicit service name for traces. Set this to `omegle-phoenix` for clearer Jaeger grouping.
- **`OTEL_SERVICE_INSTANCE_ID`** (default: hostname or runtime-generated identity): optional stable per-node identity for traces.

---

## Go backend (`backend/golang`)

### Server
- **`PORT`** (default: `8082`): HTTP port.
- **`HOST`** (default: `0.0.0.0`): bind host/interface. Use `127.0.0.1` to bind locally only.
- **`ENABLE_IPV6`** (default: `false`; format: `true|false`): when `true`, binds on `::` (dual-stack) unless `HOST` is overridden.
- **`IGNORE_DOTENV`** (default: unset): if set to any non-empty value, the service will not auto-load a local `.env` file (useful in containers).

### Security / shared secrets
- **`SHARED_SECRET`** (**required**): cross-service shared secret (must match Phoenix).
- **`JWT_SECRET`** (**required** for admin/combined binaries): signs admin JWTs. Must be at least 32 chars.
- **`JWT_ACCESS_EXPIRATION_MINUTES`** (default: `15`): access-token lifetime (short-lived; used on each authenticated admin API call).
- **`JWT_EXPIRATION_HOURS`** (default: `8`): refresh token / session lifetime (how long a moderator/admin stays signed in).

### CORS / proxy trust
- **`CORS_ORIGIN`** (default: empty; format: comma-separated origins): used by Go websocket/http origin checks (browser clients).
- **`TRUSTED_PROXY_CIDRS`** (default: `127.0.0.1/32,::1/128` if unset): trusted proxy CIDRs used by Gin for `ClientIP()` resolution.

### Postgres
- **`POSTGRES_HOST`** (default: `localhost`): Postgres host.
- **`POSTGRES_PORT`** (default: `5432`)
- **`POSTGRES_USER`** (default: `postgres`)
- **`POSTGRES_PASSWORD`** (default: `postgres`)
- **`POSTGRES_DB`** (default: `omegle`)
- **`POSTGRES_SSLMODE`** (default: `disable`)

### Postgres connection pool
- **`POSTGRES_MAX_OPEN_CONNS`** (default: `25`): maximum open DB connections per Go instance.
- **`POSTGRES_MAX_IDLE_CONNS`** (default: `5`): maximum idle DB connections kept in the pool.
- **`POSTGRES_CONN_MAX_LIFETIME_MINUTES`** (default: `30`): recycle connections to avoid stale/broken connections (important with proxies).
- **`POSTGRES_CONN_MAX_IDLE_MINUTES`** (default: `10`): close idle connections to reduce DB load.

### Redis cluster
- **`REDIS_CLUSTER_NODES`** (**required**; format: comma-separated `host:port`): cluster seed list (used for discovery).
- **`REDIS_PASSWORD`** (default: empty): Redis auth password.
- **`REDIS_POOL_SIZE`** (default: go-redis cluster default): optional per-node pool size override for the Go Redis client. If unset, go-redis v9 uses its built-in cluster default based on `GOMAXPROCS`.

### Ban sync behavior
- **`BAN_SYNC_INTERVAL_SECONDS`** (default: `0`/disabled): if > 0, periodically resyncs active bans from Postgres → Redis.
  - Use this only if you expect Redis to lose state unexpectedly; otherwise rely on startup sync + event-driven updates to reduce DB/Redis load.

### Auto-Moderation API Integration

> For full documentation on provider compatibility, supported adapters, dual assessment modes, and how to add new models, see [`MODERATION.md`](MODERATION.md).

- **`AUTO_MODERATION_ENABLED`** (default: `false`): Enables the background worker for LLM-based report evaluations.
- **`AUTO_MODERATION_NIM_API_KEY`** (or **`NIM_API_KEY`**): API key for the inference provider. Works with any OpenAI-compatible API — not limited to NVIDIA NIM.
- **`AUTO_MODERATION_NIM_BASE_URL`** (default: `https://integrate.api.nvidia.com/v1`): The base endpoint URL utilized by the native API client interface to dispatch moderation HTTP operations.
- **`AUTO_MODERATION_MODEL`** (default: `nvidia/llama-3.1-nemotron-safety-guard-8b-v3`): The distinct API target endpoint identification tag (e.g. `gpt-4` or `deepseek-chat`).
- **`AUTO_MODERATION_MODEL_TYPE`** (default: cascades to `AUTO_MODERATION_MODEL`): Configures the local parser mapping handling prompt injections and safety interpretations. Supported types:
  - `generic-json` (best for StepFun, OpenAI, DeepSeek, Qwen, Claude and other not purpose built LLMs)
  - `meta/llama-guard-4-12b`
  - `nvidia/llama-3.1-nemotron-safety-guard-8b-v3`
  - `nvidia/llama-3.1-nemotron-safety-guard-multilingual-8b-v1`
  - `nvidia/llama-3.1-nemoguard-8b-content-safety`
  - `nvidia/nemotron-content-safety-reasoning-4b`
- **`AUTO_MODERATION_TEMPERATURE`** (default: `0.5`): Specifies the sampling temperature. Non-reasoning specialized classifiers typically expect `0.0`, while reasoning models require `0.5` or higher.
- **`AUTO_MODERATION_MAX_TOKENS`** (default: `8192`): Dictates the maximum amount of output tokens the model is permitted to generate. Must be set high (e.g. `8192`) when using reasoning models.
- **`AUTO_MODERATION_DEBUG`** (default: `false`): When enabled, prints the full prompt, token usage stats, and raw model response to stdout for every moderation assessment. Keep disabled in production.
- **`AUTO_MODERATION_ENQUEUE_TIMEOUT_MS`** (default: `250`): how long report creation waits when waking the background worker before falling back to the normal periodic sweep.

### Root admin bootstrap
- **`ROOT_ADMIN_USERNAME`** (default: `admin`): initial “root” admin username created/ensured at startup.
- **`ROOT_ADMIN_PASSWORD`** (**required** in practice): initial “root” admin password. Rotate after bootstrapping.

### Cloudflare TURN credentials
- **`CLOUDFLARE_TURN_KEY_ID`** / **`CLOUDFLARE_TURN_API_TOKEN`** (optional): enables `/api/v1/webrtc/turn` to mint ephemeral relay credentials via Cloudflare Calls.
  - If missing, the TURN endpoint falls back to STUN-only ICE servers (P2P may still work, but NAT traversal will be worse).

### Pairline TURN mode
- **`TURN_MODE`** (default: `cloudflare`): selects TURN behavior for the Go WebRTC bootstrap endpoint.
  - `cloudflare`: use Cloudflare Calls TURN credentials.
  - `integrated`: return Pairline-managed credentials and expect the dedicated Pion TURN relay binary/container to be running.
  - `off`: disable relay credentials and return STUN-only ICE servers.
- **`TURN_REALM`** (default: `pairline`): TURN realm used by the integrated Pion relay.
- **`TURN_STUN_SERVERS`** (default: `stun:stun.cloudflare.com:3478,stun:stun.l.google.com:19302`): comma-separated STUN servers always returned by `/api/v1/webrtc/turn`.
- **`TURN_SERVER_URLS`** (default: unset): optional explicit TURN URLs advertised to browsers in integrated mode.
- **`TURN_STATIC_AUTH_SECRET`** (default: `pairline-turn-session`): static password paired with the integrated TURN username format. Live authorization still depends on Redis-backed session validation.
- **`TURN_PUBLIC_IP`**: required for the dedicated Pion relay in integrated mode; this is the IP advertised in allocations.
- **`TURN_RELAY_ADDRESS`** (default: `0.0.0.0`): local bind address used for relay sockets.
- **`TURN_UDP_LISTEN_ADDRESS`** (default: `:3478`): UDP TURN/STUN listener for the integrated relay.
- **`TURN_TCP_LISTEN_ADDRESS`** (default: unset): optional TCP TURN listener.
- **`TURN_TLS_LISTEN_ADDRESS`** (default: unset): optional TLS/TURNS listener.
- **`TURN_HEALTH_LISTEN_ADDRESS`** (default: unset): optional internal HTTP bind for the turn-only binary health endpoint (`/health`), useful for Docker infra-health checks.
- **`TURN_TLS_CERT_FILE`** / **`TURN_TLS_KEY_FILE`**: required when `TURN_TLS_LISTEN_ADDRESS` is set.
- **`TURN_RELAY_MIN_PORT`** / **`TURN_RELAY_MAX_PORT`** (defaults: `49152` / `49252`): relay allocation port range for the integrated relay.
- **`TURN_MAX_ALLOCATIONS_PER_SESSION`** (default: `4`): coarse integrated relay allocation cap used to avoid runaway allocation growth.

### Redis client compatibility
- **`REDIS_MAINT_NOTIFICATIONS_MODE`** (default: `disabled`): controls whether the Go Redis client sends `CLIENT MAINT_NOTIFICATIONS` on connect. Use `disabled` for Valkey or older Redis servers, `auto` to probe support safely, or `enabled` only when you know the server supports it.

### Root-only infra health dashboard
- **`ADMIN_HEALTH_PHOENIX_URLS`** (default: unset in code): comma-separated Phoenix health endpoints the Go admin service should poll for topology and node details. In Docker, set this explicitly on the admin service.
- **`ADMIN_HEALTH_GO_URLS`** (default: unset in code): comma-separated Go service health endpoints the admin dashboard should poll. In Docker, set this explicitly on the admin service.
- **`ADMIN_HEALTH_TURN_URLS`** (default: unset in code): optional comma-separated turn-only health endpoints the admin dashboard should poll alongside the rest of the Go fleet.
- **`OTEL_COLLECTOR_HEALTH_URL`** (default: unset in code): health endpoint used to confirm the OTLP collector is reachable from the admin service. In Docker, set this explicitly on the admin service.

### OpenTelemetry tracing
- **`OTEL_EXPORTER_OTLP_ENDPOINT`** (default: unset): base OTLP endpoint for traces and metrics, typically your collector such as `http://otel-collector:4318`. When unset, Go telemetry stays disabled.
- **`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`** (default: unset): trace-specific endpoint, for example `http://otel-collector:4318/v1/traces`. This is useful when your collector uses a non-default path.
- **`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`** (default: unset): metric-specific endpoint, for example `http://otel-collector:4318/v1/metrics`.
- If you send traces directly to Jaeger, prefer `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` instead of pointing the shared base endpoint at Jaeger, because metrics should still go to an OTLP collector.
- **`OTEL_EXPORTER_OTLP_INSECURE`** (default: exporter default; common dev value: `true`): use plaintext HTTP for local/dev collectors.
- **`OTEL_ENVIRONMENT`** (default: `development`): emitted as `deployment.environment` on spans.
- **`OTEL_SERVICE_INSTANCE_ID`** (default: hostname): optional override for instance identity if hostnames are ephemeral or not unique enough.
