# Pairline Developer Onboarding Guide

Welcome to the **Pairline** project! This document is designed to get you up to speed on the architecture, workflows, and development philosophies of our ecosystem so you can start contributing quickly. 

Onboarding has become far simpler due to Docker. Please make sure you have [Docker installed](https://docs.docker.com/engine/install/) in your development environment before proceeding.

---

## 1. High-Level Architecture

Pairline is an anonymous video and text chat platform built for **extreme horizontal scalability**. To achieve this, the system is polyglot, leveraging the best tool for each specific job:

1. **Frontend (`frontend/client` & `frontend/admin`)**
   - **Tech Stack:** React 19, Vite, TailwindCSS, WebRTC.
   - **Philosophy:** Components should be fully isolated and testable via Storybook without requiring live backend connections. Stateful logic (WebRTC peer connections, WebSocket handlers) is heavily abstracted into custom hooks (`usePhoenixSocket`, `useWebRtc`).

2. **Matchmaking Core (`backend/elixir/omegle_phoenix`)**
   - **Tech Stack:** Elixir, Phoenix, Erlang/OTP (BEAM).
   - **Philosophy:** Handles millions of persistent WebSocket connections and geographic/interest-based matchmaking shards. The BEAM VM isolates every concurrent user into their own lightweight process, entirely avoiding traditional thread-blocking and race conditions.

3. **Signaling & Moderation API (`backend/golang`)**
   - **Tech Stack:** Go 1.21+, Fiber.
   - **Philosophy:** Handles heavy data-lifting, authenticated admin routes, user reports, IP bans, and WebRTC SDP (Session Description Protocol) signaling. It utilizes Go's blistering fast `goroutines` and a 256-shard lock array to route WebRTC metadata via Redis Pub/Sub without mutex contention.

4. **Infrastructure Layer**
   - **Redis Cluster:** The single source of truth for ephemeral state. Holds matchmaking queues, active session locks, rate-limit buckets, and distributes Pub/Sub messages across Go nodes.
   - **PostgreSQL:** Persists relational, long-term data (Admin credentials, User Reports, Network Bans).

---

## 2. Bootstrapping Your Environment

Instead of manually installing Postgres and configuring a complex Redis Cluster locally, use the integrated Docker stack for infrastructure:

1. Start databases: `docker compose -f docker/docker-compose.yml up -d redis-node-1 redis-node-2 redis-node-3 redis-node-4 redis-node-5 redis-node-6 redis-cluster-init postgres`
2. Follow the detailed command/port reference in [`SETUP.md`](./SETUP.md) for generating local `.env` files and starting the respective backend binaries.

> **Note:** For everyday UI development, you don't even need the backend running! We rely extensively on Storybook.

---

## 3. Standard Development Workflows

### Frontend UI / Feature Development
If you are building a new UI component or tweaking layouts:
1. Navigate to the relevant frontend (`cd frontend/client` or `cd frontend/admin`).
2. Run `npm run storybook`.
3. Build your component in isolation using Mock States (e.g., `VideoChat.stories.tsx`). We inject dummy `Refs` and `__mockState` to simulate backend interaction.
4. Only run the full integration stack (`npm run dev`) when validating final end-to-end functionality.

### Backend Route / API Expansion
If you are adding a new moderation feature or HTTP API:
1. Navigate to `cd backend/golang`.
2. Add your route to `cmd/public/main.go` or `cmd/admin/main.go`.
3. Implement the logic in `internal/handlers/`.
4. Test locally by running `go test ./...`.

### Matchmaking Algorithm Tweaks
If you are modifying how users match (e.g., adding geographic restrictions):
1. Navigate to `cd backend/elixir/omegle_phoenix`.
2. Core matchmaking logic lives in `lib/omegle_phoenix/matchmaker.ex`.
3. WebSocket lifecycle handlers live in `lib/omegle_phoenix_web/channels/room_channel.ex`.
4. Validate changes strictly with `mix test`.

---

## 4. Testing Core Philosophy

We don't deploy until the automated suite passes. Run the entire multi-language integration suite using our Docker helper:

```bash
# From the repository root
./docker/run-tests.sh
```

This script will sequentially execute:
1. **Elixir Unit Tests:** Validates Matchmaking sharding mathematics in memory.
2. **Live Redis Integration:** Validates Elixir's ability to lock and write to the real Redis cluster.
3. **Redis Stress Harness:** Simulates thousands of synthetic connects/disconnects natively.
4. **Golang Tests:** Validates HTTP handlers and sharded Pub/Sub logic natively on the host.

---

## 5. Security & Contribution Guidelines
- **NEVER** expose the `/health` or `/api/health` endpoints to the public internet; they reveal internal routing matrices.
- Keep the `SHARED_SECRET` strictly internal to Docker networks.
- Before committing, ensure you run `npm run lint` locally for frontends, and that Go files are formatted via `go fmt`.

You're all set. Welcome aboard!
