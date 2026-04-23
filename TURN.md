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

The TURN worker emits OpenTelemetry traces and Prometheus metrics (see `OTEL_EXPORTER_OTLP_ENDPOINT`).

Key metrics:
- Allocation limits and usage
- Denial categories (unmatched session, banned session, invalid token)
- Transport mix (UDP, TCP, TLS)
- Bytes relayed

*Note on Docker Host Networking: When running the TURN worker in Docker with `network_mode: host`, the worker cannot resolve internal Docker bridge DNS names (like `otel-collector`). The `OTEL_EXPORTER_OTLP_ENDPOINT` must be set to `http://127.0.0.1:4318` assuming the collector publishes that port to the host.*
