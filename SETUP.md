# Setup

> **Note:** New to the project? Read the [Developer Onboarding Guide](./ONBOARDING.md) first for a high-level architectural overview and daily development workflows before running these commands!

This project runs as four app services plus Redis and Postgres:

- `frontend/client`: React + Vite chat app on `http://localhost:5173`
- `frontend/admin`: React + Vite admin app on `http://localhost:5174`
- `backend/elixir/omegle_phoenix`: Phoenix websocket/matchmaking service on `http://localhost:8080`
- `backend/golang`: Go public/admin services, with the legacy combined binary on `http://localhost:8082`
- `redis`: local Redis Cluster seed nodes on `localhost:7000` through `localhost:7005`
- `postgres`: moderation data on `localhost:5432`

## Prerequisites

- Node.js 20+
- Go 1.21+
- Elixir 1.17+ with Erlang/OTP 26+
- Docker with Docker Compose, or local Redis 7+ and PostgreSQL 15+

## 1. Start Redis and Postgres

Using the checked-in Docker cluster stack:

```bash
docker compose -f docker/docker-compose.yml up -d redis-node-1 redis-node-2 redis-node-3 redis-node-4 redis-node-5 redis-node-6 redis-cluster-init postgres
```

Or start local services manually with a Redis Cluster available on the ports above.

## 2. Configure the services

## Environment variables reference

Backend environment variables are documented in [`ENVIRONMENT.md`](./ENVIRONMENT.md) (Phoenix + Go consolidated reference).

Frontend:

```bash
cd frontend/client
cp .env.example .env
```

Optional admin frontend:

```bash
cd frontend/admin
cp .env.example .env
```

Phoenix backend:

```bash
cd backend/elixir/omegle_phoenix
cp .env.example .env
```

Go backend:

```bash
cd backend/golang
cp .env.example .env
```

Minimum values to review:

- `frontend/client/.env`
  - `VITE_API_URL=http://localhost:8082`
  - `VITE_WS_URL=ws://localhost:8080/ws`
- `frontend/admin/.env`
  - `VITE_API_URL=http://localhost:8082`
  - `VITE_ADMIN_BASE_PATH=/`
- `backend/elixir/omegle_phoenix/.env`
  - `SECRET_KEY_BASE`
  - `SHARED_SECRET`
  - `CORS_ORIGINS`
  - `TRUSTED_PROXY_CIDRS`
  - `ADMIN_STREAM`
  - `MATCH_EVENT_STREAM`
  - `MATCH_FRONTIER_SIZE`
  - `MATCH_SHARD_COUNT`
  - `MATCH_RELAXED_WAIT_MS`
  - `MATCH_OVERFLOW_WAIT_MS`
- `backend/golang/.env`
  - `POSTGRES_*`
  - `JWT_SECRET`
  - `SHARED_SECRET`
  - `ROOT_ADMIN_PASSWORD`
  - `CORS_ORIGIN`
  - `TRUSTED_PROXY_CIDRS`
  - `BAN_SYNC_INTERVAL_SECONDS`

`SHARED_SECRET` should match across the backend services.

Notes:

- Phoenix matchmaking now uses exact-interest queues, relaxed interest tiers, and frontier-limited queue scans. `MATCH_FRONTIER_SIZE` controls how much of each queue head is searched per pass, `MATCH_SHARD_COUNT` controls relaxed/random shard fanout, `MATCH_RELAXED_WAIT_MS` controls when users can relax out of exact-interest buckets, and `MATCH_OVERFLOW_WAIT_MS` controls when they can fall back to broad random matching.
- Phoenix shard coordination now uses a Redis Stream. `MATCH_EVENT_STREAM` controls the stream name, while `MATCH_SWEEP_INTERVAL_MS` and `MATCH_SWEEP_STALE_AFTER_MS` tune the slower stale-queue safety sweep.
- Phoenix admin moderation fanout now uses a Redis Stream by default for durable cross-node coordination. `ADMIN_STREAM` controls the stream name.
- The Go service now does startup ban reconciliation by default. Set `BAN_SYNC_INTERVAL_SECONDS` to a positive value only if you want periodic full resyncs.

## 3. Install dependencies

Frontend:

```bash
cd frontend/client
npm install
```

Optional admin frontend:

```bash
cd frontend/admin
npm install
```

Phoenix backend:

```bash
cd backend/elixir/omegle_phoenix
mix deps.get
```

Go backend:

```bash
cd backend/golang
go mod download
```

## 4. Run the services

Phoenix backend:

```bash
cd backend/elixir/omegle_phoenix
mix phx.server
```

Go backend:

```bash
cd backend/golang
go run .
```

Optional split-binary flow:

```bash
cd backend/golang
go run ./cmd/public
go run ./cmd/admin
```

Frontend:

```bash
cd frontend/client
npm run dev
```

Admin frontend:

```bash
cd frontend/admin
npm run dev
```

## 5. Verify

- Chat frontend: open `http://localhost:5173`
- Admin frontend: open `http://localhost:5174`
- Phoenix health: `http://localhost:8080/api/health`
- Go health: `http://localhost:8082/health`

If you are using the Docker stack through Nginx instead of running the services
directly on the host, note that `/api/health` and `/health` are intentionally
not exposed publicly. Use the authenticated admin infra endpoint or inspect the
services from inside the Docker network.

## Useful commands

Frontend:

```bash
cd frontend/client
npm run build
npm run lint
```

Admin frontend:

```bash
cd frontend/admin
npm run build
npm run lint
```

Go backend:

```bash
cd backend/golang
GOCACHE=/tmp/go-build go test ./...
```

Phoenix backend:

```bash
cd backend/elixir/omegle_phoenix
mix compile
mix test
```
