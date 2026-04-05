# Setup

This project runs as three app services plus Redis and Postgres:

- `frontend`: React + Vite on `http://localhost:5173`
- `backend/elixir/omegle_phoenix`: Phoenix websocket/matchmaking service on `http://localhost:8080`
- `backend/golang`: Go moderation and TURN service on `http://localhost:8082`
- `redis`: session and pub/sub state on `localhost:6379`
- `postgres`: moderation data on `localhost:5432`

## Prerequisites

- Node.js 20+
- Go 1.21+
- Elixir 1.17+ with Erlang/OTP 26+
- Docker with Docker Compose, or local Redis 7+ and PostgreSQL 15+

## 1. Start Redis and Postgres

Using Docker Compose:

```bash
docker compose up -d redis postgres
```

Or start local services manually on the default ports above.

## 2. Configure the services

Frontend:

```bash
cd frontend
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

- `frontend/.env`
  - `VITE_API_URL=http://localhost:8082`
  - `VITE_WS_URL=ws://localhost:8080/ws`
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
cd frontend
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

Frontend:

```bash
cd frontend
npm run dev
```

## 5. Verify

- Frontend: open `http://localhost:5173`
- Phoenix health: `http://localhost:8080/health`
- Go health: `http://localhost:8082/health`

## Useful commands

Frontend:

```bash
cd frontend
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
