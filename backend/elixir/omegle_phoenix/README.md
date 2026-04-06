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
- `ADMIN_STREAM`
- `ADMIN_STREAM_BLOCK_MS`
- `ADMIN_STREAM_BATCH_SIZE`
- `MATCH_TIMEOUT`
- `MATCH_BATCH_SIZE`
- `MATCH_FRONTIER_SIZE`
- `MATCH_SWEEP_INTERVAL_MS`
- `MATCH_SWEEP_STALE_AFTER_MS`
- `MATCH_EVENT_STREAM`
- `MATCH_EVENT_STREAM_BLOCK_MS`
- `MATCH_EVENT_STREAM_BATCH_SIZE`
- `MATCH_EVENT_STREAM_MAXLEN`
- `MATCH_SHARD_COUNT`
- `MATCH_RELAXED_WAIT_MS`
- `MATCH_OVERFLOW_WAIT_MS`
- `REAPER_INTERVAL_MS`
- `REAPER_BATCH_SIZE`
- `NODE_NAME`
- `NODE_COOKIE`
- `CLUSTER_NODES`

`MATCH_FRONTIER_SIZE` caps how deep the matcher searches within a queue before it defers to the next stream-triggered pass. `MATCH_SHARD_COUNT` controls the relaxed-tier and random-queue shard fanout, `MATCH_RELAXED_WAIT_MS` controls when interest users can relax from exact buckets into the relaxed tier, and `MATCH_OVERFLOW_WAIT_MS` controls when they are allowed into broad random fallback. `MATCH_EVENT_STREAM` is the Redis Stream used for cross-node shard coordination, while `MATCH_SWEEP_INTERVAL_MS` and `MATCH_SWEEP_STALE_AFTER_MS` keep the fallback sweep focused on quiet queues instead of rescanning hot queues constantly.
Admin moderation fanout now uses Redis Streams for durable cross-node delivery, with `ADMIN_STREAM` as the stream name and the block/batch knobs controlling consumer polling behavior.

## Useful commands

```bash
mix phx.server
mix compile
mix test
```

## Testing

Fast unit-style tests:

```bash
SECRET_KEY_BASE=test-secret \
SHARED_SECRET=test-shared \
MIX_ENV=test \
mix test --no-start
```

These cover the Elixir app logic and the Redis compatibility shim without
requiring a live Redis cluster.

Live Redis cluster integration tests:

```bash
LIVE_REDIS_CLUSTER_TESTS=1 \
REDIS_CLUSTER_NODES=127.0.0.1:7000,127.0.0.1:7001,127.0.0.1:7002 \
REDIS_PASSWORD=your-redis-password \
SECRET_KEY_BASE=test-secret \
SHARED_SECRET=test-shared \
MIX_ENV=test \
mix test test/redis_live_integration_test.exs
```

The live suite exercises the real `eredis_cluster` client against a real Redis
cluster and covers:

- Redis wrapper reply normalization
- mixed keyless/keyed pipeline handling
- session create/get/delete
- matchmaking join/leave queue
- pair/reset flows
- emergency disconnect partner recovery
- reaper orphan cleanup
- IP ban/unban flow

If your local shell cannot reach the Redis cluster directly, run the live test
inside a Phoenix container from the Docker stack instead.

Stress harness:

```bash
SECRET_KEY_BASE=test-secret \
SHARED_SECRET=test-shared \
MIX_ENV=test \
STRESS_SESSION_COUNT=1000 \
STRESS_CONCURRENCY=500 \
mix run scripts/redis_live_stress.exs
```

Useful stress env vars:

- `STRESS_SESSION_COUNT`
- `STRESS_CONCURRENCY`
- `STRESS_PAIR_COUNT`
- `STRESS_LEAVE_COUNT`
- `STRESS_DISCONNECT_COUNT`

## Main responsibilities

- websocket transport at `/ws`
- Redis-backed shared session lifecycle
- Redis-backed matchmaking queue
- cluster-aware room signaling and peer routing
- admin stream consumption for emergency moderation actions

## Multi-node Phoenix clustering

Additional Phoenix nodes can be plugged into the cluster by giving each node:

- a unique `NODE_NAME`
- the same `NODE_COOKIE`
- `NODE_DISTRIBUTION=short` for short hostnames, or `long` for FQDNs/IP-style full names
- the same `CLUSTER_NODES` list
- access to the same Redis instance

Example local two-node cluster:

```bash
# node 1
PORT=8080 NODE_NAME=phoenix1 NODE_DISTRIBUTION=short NODE_COOKIE=pairline-dev-cookie \
CLUSTER_NODES=phoenix1@localhost,phoenix2@localhost \
./start.sh

# node 2
PORT=8081 NODE_NAME=phoenix2 NODE_DISTRIBUTION=short NODE_COOKIE=pairline-dev-cookie \
CLUSTER_NODES=phoenix1@localhost,phoenix2@localhost \
./start.sh
```

For long-name distribution, use full node names instead:

```bash
PORT=8080 NODE_NAME=phoenix1@example.internal NODE_DISTRIBUTION=long NODE_COOKIE=pairline-dev-cookie \
CLUSTER_NODES=phoenix1@example.internal,phoenix2@example.internal \
./start.sh
```

After boot, `GET /api/health` will show the current BEAM node and connected peers.
