# TURN.md

This document outlines the integrated TURN relay system currently implemented in Pairline.

Platforms of this scale require robust WebRTC NAT traversal (TURN). Historically, this is often offloaded to external API-driven providers (like Twilio or Cloudflare). Pairline implements its own horizontally scalable TURN relay infrastructure built on Pion, directly integrated into the platform's session and moderation semantics. 

It covers:

- the Data Plane (TURN relay processes)
- the Control Plane (Validation gRPC API)
- the Client Auth Flow
- current network and security architecture
- deployment configuration

This file documents the code that exists today. It is not a design proposal.

## Overview

Pairline currently supports two modes for WebRTC TURN fallback:

- `cloudflare`: uses an external provider via short-lived bearer tokens (legacy/fallback)
- `integrated`: uses Pairline's internal Pion-based TURN relay infrastructure (production default)

The integrated implementation follows a split, highly-scalable model:

- `cmd/turn` (Data Plane) runs pure TURN relay workers on host networking. It is completely stateless, has no Redis/Postgres access, and is strictly responsible for UDP/TCP/TLS packet relay.
- `cmd/public` (Control Plane) exposes an internal gRPC validation API. It holds the Redis connections and owns the authoritative application state (checking if sessions exist, are active, are matched, and are not banned).
- `nginx` load-balances the internal control-plane validation traffic, preventing gRPC connection bottlenecks.

## Code Map

Main files:

- Data Plane (Relay):
  - `backend/golang/cmd/turn/main.go`
  - `backend/golang/internal/turnservice/service.go`
  - `backend/golang/internal/turnservice/config.go`
- Control Plane (Validation):
  - `backend/golang/internal/turncontrol/server.go`
  - `backend/golang/internal/turncontrol/auth.go`
- Bootstrap API:
  - `backend/golang/internal/handlers/turn_bootstrap.go`
- Proxy / Topology:
  - `docker/nginx/pairline.conf`

## Current Architecture

### 1. Data Plane (TURN Workers)

The TURN relay operates on the edge, typically deployed with host networking (`network_mode: host` in Docker) to allow direct UDP/TCP binds for the relay ephemeral port range.

At runtime:
1. The worker listens on standard STUN/TURN ports (`:3478` UDP/TCP, `:5349` TLS).
2. It dynamically reserves ports from a configured relay range (default: `49152-49252`).
3. It maintains a pool of persistent gRPC connections (`TURN_CONTROL_GRPC_POOL_SIZE`, default 4) to the internal Nginx load balancer (`127.0.0.1:50050`).
4. It intercepts all incoming Pion STUN/TURN `Allocate` and `CreatePermission` requests.
5. It delegates the cryptographic and semantic authorization decision over gRPC.

### 2. Control Plane (Go Public Service)

The public Go service operates the internal gRPC `turnControlValidationServer`. 

At runtime:
1. It validates the incoming gRPC call's authentication using `TURN_CONTROL_GRPC_SHARED_SECRET`.
2. It unpacks the client's TURN username (which is a composite of `session_id|sha256(session_token)`).
3. It verifies against Redis that the session is real and active.
4. It ensures the session is currently paired in a match.
5. It verifies the session IP is not banned.
6. It enforces capacity via a Redis atomic Lua script (`turn:allocations:{session_id}`).
7. It returns `Allowed: true` or a specific denial reason.

### 3. Nginx gRPC Load Balancing

Nginx sits between the Data Plane and Control Plane.
1. It exposes a single gRPC upstream on `:50050`.
2. It load-balances requests across all `cmd/public` instances via `random two least_conn`.
3. It specifically disables retries (`grpc_next_upstream off`) to prevent double-counting state-mutating allocations in Redis if a timeout occurs.

## Client Authentication Flow

Pairline avoids issuing secondary, long-lived TURN credentials. Instead, TURN auth is derived directly from the user's live session.

1. The React client hits `/api/v1/webrtc/turn` to fetch available TURN server URLs.
2. The client builds its TURN username locally: `session_id|sha256(session_token)`.
3. The client connects to the TURN worker using this username and the globally shared `TURN_STATIC_AUTH_SECRET`.
4. The TURN worker extracts the session ID and token hash, passing them to the control plane.
5. If the user disconnects or is banned, the Redis state changes immediately. The next TURN allocation attempt from that user will be instantly denied by the control plane.

## Configuration & Deployment

The TURN service is configured via environment variables.

### General Settings
- `TURN_MODE`: Set to `integrated` to enable the built-in relay.
- `TURN_STATIC_AUTH_SECRET`: The shared password all clients use. Not a bearer token.
- `TURN_PUBLIC_IP`: The public, routable IP address of the host running the TURN worker. Required for WebRTC candidate generation.

### Control-Plane gRPC
- `TURN_CONTROL_GRPC_LISTEN_ADDRESS`: Bind address on the public Go service (`0.0.0.0:50051`).
- `TURN_CONTROL_GRPC_ADDRESS`: Target the TURN worker dials (`127.0.0.1:50050` via Nginx).
- `TURN_CONTROL_GRPC_SHARED_SECRET`: Symmetric secret for internal service-to-service gRPC calls.
- `TURN_CONTROL_GRPC_POOL_SIZE`: Connections per worker to the control plane.

## Observability

The TURN relay initializes OpenTelemetry tracing and metrics on startup via the shared `observability` package, registering as service name `pairline-go-turn`.

### Trace Spans

The relay emits distributed traces for every critical decision point in the TURN data path:

| Span Name | Kind | Description |
|-----------|------|-------------|
| `turn.auth` | Server | Fires on every STUN/TURN auth request. Records the `turn.username`, `turn.src_addr`, and on success the resolved `turn.session_id` and `turn.matched_id`. On denial, the `turn.auth.denial_reason` attribute is set and the span status is `Error`. |
| `turn.quota.reserve` | Internal | Fires when the allocation quota handler reserves a slot. Records `turn.username`, `turn.src_addr`, `turn.allocation_quota`, and the boolean `turn.quota.allowed` result. |
| `turn.allocation.release` | Internal | Fires when a TURN allocation is deleted (client disconnect or timeout). Records the `turn.username` and release success/failure. |

These spans propagate context through the gRPC control-plane call, so a single `turn.auth` span in Jaeger will show the full chain: relay → Nginx → `cmd/public` → Redis.

### Relay Metrics

The relay records the following metrics via the shared `observability` package:

| Metric | Type | Description |
|--------|------|-------------|
| `pairline.turn.relay.auth_total` | Counter | Total auth attempts, labeled by `turn.relay.allowed` and `turn.relay.denial_reason`. |
| `pairline.turn.relay.auth.duration_ms` | Histogram | Latency of auth validation calls (includes gRPC round-trip to control plane). |
| `pairline.turn.relay.quota_total` | Counter | Total quota reservation attempts, labeled by `turn.relay.quota.allowed`. |
| `pairline.turn.relay.release_total` | Counter | Total allocation releases, labeled by `turn.relay.release.success`. |

Standard runtime metrics (`pairline.runtime.*`) for heap, goroutines, CPU, and GC are also emitted.

### Health Endpoint Authentication

The TURN relay health endpoint (`/health`) is protected by `GOLANG_TURN_SHARED_SECRET`. When this environment variable is set, any request must include a matching `x-shared-secret` header or receive a `401 Unauthorized` response.

The admin dashboard reads `GOLANG_TURN_SHARED_SECRET` from its own environment and attaches it automatically when polling `ADMIN_HEALTH_TURN_URLS`.

### OTLP Export Authentication

All services (including TURN workers) authenticate their trace and metric exports to the OpenTelemetry Collector using bearer token authentication.

- **Collector side:** The `bearertokenauth` extension is configured on the OTLP gRPC (`:4317`) and HTTP (`:4318`) receivers. The expected token is read from `OTEL_COLLECTOR_SHARED_SECRET`.
- **Client side:** Each service sets `OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer <token>`. The Go and Elixir OpenTelemetry SDKs parse this standard environment variable and attach the header to every export payload automatically.

### Host Networking Caveat

When running the TURN worker in Docker with `network_mode: host`, the worker cannot resolve internal Docker bridge DNS names (like `otel-collector`). There are two deployment options:

1. **Expose the collector OTLP ports to localhost:** Add `127.0.0.1:4318:4318` to the collector's `ports:` mapping and set `OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318` on the TURN workers. If you also have host-side OTLP/gRPC clients, expose `127.0.0.1:4317:4317` on the collector too. This is safe because the collector's `bearertokenauth` extension rejects unauthenticated requests.
2. **Route through an external tunnel:** Point `OTEL_EXPORTER_OTLP_ENDPOINT` at an authenticated external URL (e.g. a Cloudflare tunnel fronting the collector). In this case, remove `OTEL_EXPORTER_OTLP_INSECURE=true` to enforce strict TLS verification.

The checked-in local `docker/docker-compose.yml` uses option 1 for the host-network TURN workers.
