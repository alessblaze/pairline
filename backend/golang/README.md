# Go Backend

Go services for public WebRTC/report traffic and separate admin APIs.

## Local development

Use the shared setup guide in [../../SETUP.md](../../SETUP.md) for the full stack, then run:

```bash
go mod download
go run .
```

The legacy combined binary defaults to port `8082`.

## Binary layout

- `go run .`
  Starts the legacy combined binary with both public and admin routes.
- `go run ./cmd/public`
  Starts the public binary only:
  - `GET /health`
  - `POST /api/v1/moderation/report`
  - `GET /api/v1/webrtc/ws`
  - `POST /api/v1/webrtc/turn`
  - optional internal TURN control-plane gRPC when `TURN_CONTROL_GRPC_LISTEN_ADDRESS` is set
- `go run ./cmd/turn`
  Starts the dedicated Pion TURN relay binary. It requires `TURN_MODE=integrated`.
  When `TURN_CONTROL_GRPC_ADDRESS` is set, the relay validates sessions through
  the public Go service instead of reading Redis directly.
- `go run ./cmd/admin`
  Starts the admin binary only:
  - `GET /health`
  - `POST /api/v1/admin/login`
  - `POST /api/v1/admin/refresh`
  - `POST /api/v1/admin/logout`
  - `GET /api/v1/admin/infra/health`
  - `GET /api/v1/admin/reports`
  - `POST /api/v1/admin/ban`
  - account-management routes

## Environment

Copy `.env.example` to `.env` and review:

- `POSTGRES_*`
- `REDIS_*`
- `SHARED_SECRET`
- `JWT_SECRET`
- `JWT_ACCESS_EXPIRATION_MINUTES`
- `ROOT_ADMIN_USERNAME`
- `ROOT_ADMIN_PASSWORD`
- `CORS_ORIGIN`
- `TRUSTED_PROXY_CIDRS`
- `BAN_SYNC_INTERVAL_SECONDS`
- `AUTO_MODERATION_*`
- `NIM_API_KEY`
- `TURN_*`
- `CLOUDFLARE_TURN_*`

For the full backend env var reference (Phoenix + Go), see [`ENVIRONMENT.md`](../../ENVIRONMENT.md).

`JWT_SECRET` is only required for the admin or combined binaries.
`JWT_ACCESS_EXPIRATION_MINUTES` defaults to `15` and controls the short-lived access token used for authenticated admin/moderator requests.
`JWT_EXPIRATION_HOURS` now acts as the refresh-token/session lifetime for admin and moderator accounts.
`BAN_SYNC_INTERVAL_SECONDS` defaults to disabled behavior when unset or `0`, which means startup reconciliation plus event-driven Redis updates only.
`AUTO_MODERATION_ENABLED` defaults to `false` and acts as the fallback toggle until an admin updates the persisted setting.
`NIM_API_KEY` (or `NVIDIA_NIM_API_KEY`) enables the async report auto-moderation worker to call NVIDIA NIM after reports are stored.
`AUTO_MODERATION_MODEL` defaults to `nvidia/llama-3.1-nemotron-safety-guard-8b-v3`.
`AUTO_MODERATION_ENQUEUE_TIMEOUT_MS` defaults to `250` and caps how long report creation waits to wake the background worker before falling back to the next periodic sweep.
The Go worker currently ships model adapters for `nvidia/llama-3.1-nemotron-safety-guard-8b-v3`, `nvidia/llama-3.1-nemotron-safety-guard-multilingual-8b-v1`, `nvidia/llama-3.1-nemoguard-8b-content-safety`, `nvidia/nemotron-content-safety-reasoning-4b`, and `meta/llama-guard-4-12b` under `internal/automod/models/`.
Go services are Redis Cluster only and require `REDIS_CLUSTER_NODES`.
`REDIS_POOL_SIZE` optionally overrides the go-redis cluster pool size per node when you need predictable tuning across deployments.
Go startup schema work is now serialized with a Postgres advisory lock so concurrent combined/public/admin boots do not all run migrations at once.
Set `IGNORE_DOTENV=1` (or any non-empty value) to skip loading a local `.env` file even if one is present in the working directory.
`ADMIN_HEALTH_PHOENIX_URLS`, `ADMIN_HEALTH_GO_URLS`, `ADMIN_HEALTH_TURN_URLS`, and `OTEL_COLLECTOR_HEALTH_URL` are used by the admin binary to build the authenticated infra-health summary consumed by the admin dashboard.
`TURN_MODE` controls relay behavior across the public bootstrap endpoint and the turn-only binary:
- `cloudflare` keeps the existing Cloudflare Calls credential flow.
- `integrated` returns Pairline-managed TURN credentials from `/api/v1/webrtc/turn` and enables `go run ./cmd/turn`.
- `off` disables relay credentials and returns STUN-only ICE servers.
When `TURN_MODE=integrated`, the bootstrap endpoint now fails closed if no usable TURN URLs are configured instead of silently returning an empty relay config.
When `TURN_MODE=cloudflare`, the bootstrap endpoint now also fails closed if the Cloudflare TURN credentials are not configured or the provider returns an invalid credential payload.
`TURN_CONTROL_GRPC_LISTEN_ADDRESS` enables an internal authenticated gRPC control-plane API on the public Go service for TURN validation.
`TURN_CONTROL_GRPC_ADDRESS` tells the turn-only binary to use that internal control-plane API instead of connecting to Redis directly.
`TURN_CONTROL_GRPC_SHARED_SECRET` must match on both sides when the TURN control-plane gRPC path is enabled.

## Useful commands

```bash
go run .
go run ./cmd/public
go run ./cmd/turn
go run ./cmd/admin
GOCACHE=/tmp/go-build go test ./...
```

## Live stress test

The Go backend now includes an opt-in live Redis signaling stress test, similar in spirit to the Phoenix Redis stress runner. It is excluded from normal `go test` runs and only executes when you opt in with the `stress` build tag plus `RUN_LIVE_REDIS_STRESS=1`.

Example:

```bash
cd backend/golang
RUN_LIVE_REDIS_STRESS=1 \
REDIS_CLUSTER_NODES=127.0.0.1:7000,127.0.0.1:7001,127.0.0.1:7002 \
GOCACHE=/tmp/go-build \
go test -tags=stress -run TestRedisSignalingLiveStress -count=1 ./internal/handlers
```

Optional knobs:

- `STRESS_SESSION_COUNT`
- `STRESS_LOCAL_SESSION_COUNT`
- `STRESS_LOCAL_SENDS_PER_SESSION`
- `STRESS_REMOTE_SENDS_PER_SESSION`
- `STRESS_CONCURRENCY`

## Main routes

- Public:
  - `GET /health`
  - `POST /api/v1/moderation/report`
  - `GET /api/v1/webrtc/ws`
  - `POST /api/v1/webrtc/turn`
- Admin:
  - `GET /health`
  - `POST /api/v1/admin/login`
  - `POST /api/v1/admin/refresh`
  - `POST /api/v1/admin/logout`
  - `GET /api/v1/admin/reports`
  - `PUT /api/v1/admin/reports/:id`
  - `GET /api/v1/admin/bans`
  - `GET /api/v1/admin/auto-moderation/settings`
  - `POST /api/v1/admin/ban`
  - `PUT /api/v1/admin/auto-moderation/settings`
  - `DELETE /api/v1/admin/ban/:session_id`

## Admin roles

- `moderator`, `admin`, and `root` can review reports and read the ban registry.
- `admin` and `root` can create or remove bans.
- `admin` and `root` can manage admin accounts, with the existing in-handler role restrictions still applied for creating or deleting higher-privilege accounts.
