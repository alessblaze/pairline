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
- **`REDIS_POOL_SIZE`** (default: `16`): connection pool size. Increase if Redis latency rises under high concurrency and you see pool exhaustion; too high can overwhelm Redis with connections.

### Session lifecycle
- **`SESSION_TTL`** (default: `3600`): how long session records stay alive in Redis after last refresh. Larger values reduce accidental session expiry but increase orphan memory.
- **`REPORT_GRACE_SECONDS`** (default: `900`): how long after disconnect a user can still report the last peer (used by moderation/report flows).

### Matchmaking (queue + streams coordination)
- **`MATCH_TIMEOUT`** (default: `30000` ms): how long a search can run before timing out.
- **`MATCH_LEADER_TTL_MS`** (default: `5000`): lease TTL for “matchmaker leader” work to avoid multiple nodes doing identical heavy passes.
- **`MATCH_BATCH_SIZE`** (default: `200`): how many queued session IDs are pulled at a time from a queue head.
- **`MATCH_FRONTIER_SIZE`** (default: `16`): caps how deep into the head of each interest bucket the matcher will look in one pass.
  - Higher increases match quality/fairness under load but increases Redis reads.
- **`MATCH_SHARD_COUNT`** (default: `8`): number of relaxed/random shards to fan out across (more shards reduces hot-spotting but increases coordination overhead).
- **`MATCH_RELAXED_WAIT_MS`** (default: `5000`): when a user can “relax” from exact interest matching into a broader relaxed tier.
- **`MATCH_OVERFLOW_WAIT_MS`** (default: `15000`): when a user can fall back to “random” matching.
- **`MATCH_EVENT_STREAM`** / **`MATCH_EVENT_STREAM_GROUP`**: stream + consumer group used for cross-node coordination (ensures nodes don’t all sweep the same shards).
- **`MATCH_EVENT_STREAM_BLOCK_MS`** / **`MATCH_EVENT_STREAM_BATCH_SIZE`**: polling behavior (lower block = more responsive but more Redis calls; larger batch = fewer calls but more bursty processing).
- **`MATCH_EVENT_STREAM_MAXLEN`**: caps stream size to bound memory (too low can drop coordination history aggressively; too high increases memory).
- **`MATCH_SWEEP_INTERVAL_MS`** / **`MATCH_SWEEP_STALE_AFTER_MS`**: safety sweep knobs for “quiet” queues so they don’t get stuck if stream-triggered events are missed.

### Reaper (orphan cleanup)
- **`REAPER_INTERVAL_MS`** (default: `10000`): how often the reaper scans for stale/orphaned sessions/queue entries.
- **`REAPER_BATCH_SIZE`** (default: `200`): work chunk size per pass. Increase for faster cleanup at the cost of more Redis load.

### Admin moderation coordination (durable stream)
- **`ADMIN_STREAM`** (default: `admin:action:stream`): stream name used to fan out admin “emergency” actions across Phoenix nodes (durable coordination).
- **`ADMIN_STREAM_GROUP`** (default: `admin:workers`): consumer group name for Phoenix nodes.
- **`ADMIN_STREAM_BLOCK_MS`** (default: `1000`): how long consumers block waiting for new admin actions.
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

### Ban sync behavior
- **`BAN_SYNC_INTERVAL_SECONDS`** (default: `0`/disabled): if > 0, periodically resyncs active bans from Postgres → Redis.
  - Use this only if you expect Redis to lose state unexpectedly; otherwise rely on startup sync + event-driven updates to reduce DB/Redis load.

### Root admin bootstrap
- **`ROOT_ADMIN_USERNAME`** (default: `admin`): initial “root” admin username created/ensured at startup.
- **`ROOT_ADMIN_PASSWORD`** (**required** in practice): initial “root” admin password. Rotate after bootstrapping.

### Cloudflare TURN credentials
- **`CLOUDFLARE_TURN_KEY_ID`** / **`CLOUDFLARE_TURN_API_TOKEN`** (optional): enables `/api/v1/webrtc/turn` to mint ephemeral relay credentials via Cloudflare Calls.
  - If missing, the TURN endpoint falls back to STUN-only ICE servers (P2P may still work, but NAT traversal will be worse).

