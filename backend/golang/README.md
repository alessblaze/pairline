# Go Backend

Go service for moderation, admin APIs, and TURN credential generation.

## Local development

Use the shared setup guide in [../../SETUP.md](../../SETUP.md) for the full stack, then run:

```bash
go mod download
go run .
```

The default port is `8082`.

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

## Useful commands

```bash
go run .
GOCACHE=/tmp/go-build go test ./...
```

## Main routes

- `GET /health`
- `POST /api/v1/moderation/report`
- `POST /api/v1/admin/login`
- `GET /api/v1/admin/reports`
- `POST /api/v1/admin/ban`
- `GET /api/v1/webrtc/turn`
