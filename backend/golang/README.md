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
  - `GET /api/v1/admin/reports`
  - `POST /api/v1/admin/ban`
  - account-management routes

## Environment

Copy `.env.example` to `.env` and review:

- `POSTGRES_*`
- `REDIS_*`
- `SHARED_SECRET`
- `JWT_SECRET`
- `ROOT_ADMIN_USERNAME`
- `ROOT_ADMIN_PASSWORD`
- `CORS_ORIGIN`
- `TRUSTED_PROXY_CIDRS`
- `BAN_SYNC_INTERVAL_SECONDS`

`JWT_SECRET` is only required for the admin or combined binaries.
`BAN_SYNC_INTERVAL_SECONDS` defaults to disabled behavior when unset or `0`, which means startup reconciliation plus event-driven Redis updates only.
Go services are Redis Cluster only and require `REDIS_CLUSTER_NODES`.

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
  - `GET /api/v1/admin/reports`
  - `POST /api/v1/admin/ban`
