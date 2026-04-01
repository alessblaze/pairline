# Phoenix Backend

Phoenix websocket and matchmaking service for anonymous text and video chat.

## Local development

Use the shared setup guide in [../../../SETUP.md](../../../SETUP.md) for the full stack, then run:

```bash
mix deps.get
mix phx.server
```

The default port is `8080`.

## Environment

Copy `.env.example` to `.env` and review:

- `REDIS_HOST`
- `REDIS_PORT`
- `SHARED_SECRET`
- `SECRET_KEY_BASE`
- `CORS_ORIGINS`
- `TRUSTED_PROXY_CIDRS`
- `ADMIN_CHANNEL`
- `NODE_NAME`
- `NODE_COOKIE`
- `CLUSTER_NODES`

## Useful commands

```bash
mix phx.server
mix compile
mix test
```

## Main responsibilities

- websocket transport at `/ws`
- Redis-backed shared session lifecycle
- Redis-backed matchmaking queue
- cluster-aware room signaling and peer routing
- admin pub/sub consumption for emergency moderation actions

## Multi-node Phoenix clustering

Additional Phoenix nodes can be plugged into the cluster by giving each node:

- a unique `NODE_NAME`
- the same `NODE_COOKIE`
- `NODE_DISTRIBUTION=short` for container/local hostnames, or `long` for FQDNs
- the same `CLUSTER_NODES` list
- access to the same Redis instance

Example local two-node cluster:

```bash
# node 1
PORT=8080 NODE_NAME=phoenix1 NODE_DISTRIBUTION=short NODE_COOKIE=pairline-dev-cookie \
CLUSTER_NODES=phoenix1@127.0.0.1,phoenix2@127.0.0.1 \
./start.sh

# node 2
PORT=8081 NODE_NAME=phoenix2 NODE_DISTRIBUTION=short NODE_COOKIE=pairline-dev-cookie \
CLUSTER_NODES=phoenix1@127.0.0.1,phoenix2@127.0.0.1 \
./start.sh
```

After boot, `GET /api/health` will show the current BEAM node and connected peers.
