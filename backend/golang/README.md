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

For the full backend env var reference (Phoenix + Go), see [`ENVIRONMENT.md`](../../ENVIRONMENT.md).

`JWT_SECRET` is only required for the admin or combined binaries.
`JWT_ACCESS_EXPIRATION_MINUTES` defaults to `15` and controls the short-lived access token used for authenticated admin/moderator requests.
`JWT_EXPIRATION_HOURS` now acts as the refresh-token/session lifetime for admin and moderator accounts.
`BAN_SYNC_INTERVAL_SECONDS` defaults to disabled behavior when unset or `0`, which means startup reconciliation plus event-driven Redis updates only.
Go services are Redis Cluster only and require `REDIS_CLUSTER_NODES`.
Set `IGNORE_DOTENV=1` (or any non-empty value) to skip loading a local `.env` file even if one is present in the working directory.
`ADMIN_HEALTH_PHOENIX_URLS`, `ADMIN_HEALTH_GO_URLS`, and `OTEL_COLLECTOR_HEALTH_URL` are used by the admin binary to build the authenticated infra-health summary consumed by the admin dashboard.

## Useful commands

```bash
go run .
go run ./cmd/public
go run ./cmd/admin
GOCACHE=/tmp/go-build go test ./...
```

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
  - `POST /api/v1/admin/ban`
  - `DELETE /api/v1/admin/ban/:session_id`

## Admin roles

- `moderator`, `admin`, and `root` can review reports and read the ban registry.
- `admin` and `root` can create or remove bans.
- `admin` and `root` can manage admin accounts, with the existing in-handler role restrictions still applied for creating or deleting higher-privilege accounts.
