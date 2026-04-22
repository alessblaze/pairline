# Docker Cluster Test Setup

This folder contains a local end-to-end Docker Compose stack for testing:

- a 3-node Elixir/Phoenix cluster
- a 2-node public Go backend
- a separate admin Go backend
- dedicated turn-only Go relay containers
- a 6-node Valkey/Redis Cluster
- Postgres
- Nginx ingress ports split by backend

## What it starts

- `nginx` on:
  - `http://localhost:8080` for Phoenix
  - `http://localhost:8081` for Go
- `redis-node-1` through `redis-node-6`
- `redis-cluster-init`
- `postgres`
- `phoenix1`
- `phoenix2`
- `phoenix3`
- `golang-public-1`
- `golang-public-2`
- `golang-admin`
- `golang-turn-1`
- `golang-turn-2`

Nginx is the only exposed ingress layer. Phoenix and Go stay internal to the
Docker network, while host access is split cleanly by port.

All Phoenix nodes share:

- the same Redis Cluster seed list
- the same Redis password: `pairline-dev-redis-password`
- the same Turnstile test secret: `1x0000000000000000000000000000000AA`
- the same Erlang cookie
- the same cluster node list
- the same explicit Docker network: `pairline_cluster`

Static IP assignments on `172.30.0.0/24`:

- `nginx` -> `172.30.0.10`
- `phoenix1` -> `172.30.0.11`
- `phoenix2` -> `172.30.0.12`
- `phoenix3` -> `172.30.0.13`
- `redis-node-1` -> `172.30.0.20`
- `redis-node-2` -> `172.30.0.21`
- `redis-node-3` -> `172.30.0.22`
- `redis-node-4` -> `172.30.0.23`
- `redis-node-5` -> `172.30.0.24`
- `redis-node-6` -> `172.30.0.25`
- `redis-cluster-init` -> `172.30.0.26`
- `postgres` -> `172.30.0.30`
- `golang-public-1` -> `172.30.0.40`
- `golang-public-2` -> `172.30.0.42`
- `golang-admin` -> `172.30.0.41`
- `golang-turn-1` -> `172.30.0.43`
- `golang-turn-2` -> `172.30.0.44`

Internal app ports:

- `phoenix1` -> `8080`
- `phoenix2` -> `8080`
- `phoenix3` -> `8080`
- `golang-public-1` -> `8081`
- `golang-public-2` -> `8081`
- `golang-admin` -> `8082`
- `golang-turn-1` -> TURN `3478` plus relay range `49152-49252`, health `8090`
- `golang-turn-2` -> TURN `3479` plus relay range `49253-49353`, health `8091`
- `nginx` -> exposed on host `8080` and `8081`

Direct TURN ingress is exposed on the host for local browser testing:

- `127.0.0.1:3478` UDP/TCP via `golang-turn-1`
- `127.0.0.1:3479` UDP/TCP via `golang-turn-2`

All Go services share the same Redis Cluster and Postgres backing services.

## Start the cluster

From the repo root:

```bash
docker compose -f docker/docker-compose.yml up --build
```

Or detached:

```bash
docker compose -f docker/docker-compose.yml up -d
```

## Check cluster health

Regular service health endpoints stay internal to the Docker network and are no
longer exposed through Nginx.

Inspect Phoenix directly from inside a Phoenix container:

```bash
docker compose -f docker/docker-compose.yml exec phoenix1 curl -s http://localhost:8080/api/health
docker compose -f docker/docker-compose.yml exec phoenix2 curl -s http://localhost:8080/api/health
docker compose -f docker/docker-compose.yml exec phoenix3 curl -s http://localhost:8080/api/health
```

Inspect Go services directly from inside the Docker network:

```bash
docker compose -f docker/docker-compose.yml exec golang-admin curl -s http://localhost:8082/health
docker compose -f docker/docker-compose.yml exec golang-public-1 curl -s http://localhost:8081/health
docker compose -f docker/docker-compose.yml exec golang-public-2 curl -s http://localhost:8081/health
docker compose -f docker/docker-compose.yml exec golang-turn-1 curl -s http://localhost:8090/health
docker compose -f docker/docker-compose.yml exec golang-turn-2 curl -s http://localhost:8091/health
```

The authenticated cross-service infra summary lives on the admin API:

```text
GET /api/v1/admin/infra/health
```

That route is intended for authenticated admin users and is what powers the
admin dashboard health panels for Postgres, Redis, Phoenix, Go, and the OTEL
collector, including turn-only relay containers when configured.

## Run tests

Build the local Phoenix image used by the Docker stack:

```bash
./docker/build-container.sh
```

Run the Elixir unit-style test suite from the host:

```bash
cd backend/elixir/omegle_phoenix
SECRET_KEY_BASE=test-secret \
SHARED_SECRET=test-shared \
MIX_ENV=test \
mix test --no-start
```

Run the live Redis integration suite inside `phoenix1` so it uses the running
cluster and container env:

```bash
docker compose -f docker/docker-compose.yml exec phoenix1 bash -lc '
  cd /app &&
  LIVE_REDIS_CLUSTER_TESTS=1 \
  SECRET_KEY_BASE=test-secret \
  SHARED_SECRET=test-shared \
  MIX_ENV=test \
  mix test test/redis_live_integration_test.exs
'
```

That live suite covers:

- real `eredis_cluster` communication
- Redis wrapper normalization and mixed pipeline behavior
- session create/get/delete
- matchmaking join/leave queue
- pair/reset flows
- emergency disconnect partner recovery
- reaper orphan cleanup
- IP ban/unban

Run the combined stress harnesses:

```bash
./docker/run-tests.sh
```

By default that script:

- builds the local Phoenix image if it is missing
- starts `docker/docker-compose.yml` if `phoenix1` is not already running
- shows named ExUnit tests for the unit and live stages
- then runs the full Elixir test stack

It includes:

- Elixir unit tests
- live Redis integration tests
- the Redis stress harness
- the Go Redis/WebRTC signaling live stress harness

The Elixir stress stage uses the existing `STRESS_*` env vars.
The Go stress stage runs inside `golang-public-1` and uses separate `GO_STRESS_*` env vars so it can be tuned independently from the Phoenix load profile.

Override the load parameters with env vars:

```bash
STRESS_SESSION_COUNT=1000 \
STRESS_CONCURRENCY=500 \
STRESS_PAIR_COUNT=200 \
STRESS_LEAVE_COUNT=320 \
STRESS_DISCONNECT_COUNT=100 \
./docker/run-tests.sh
```

Override the Go live stress parameters separately:

```bash
GO_STRESS_SESSION_COUNT=24000 \
GO_STRESS_LOCAL_SESSION_COUNT=12000 \
GO_STRESS_LOCAL_SENDS_PER_SESSION=80 \
GO_STRESS_REMOTE_SENDS_PER_SESSION=128 \
GO_STRESS_CONCURRENCY=12000 \
./docker/run-tests.sh
```

Run only selected stages:

```bash
RUN_STRESS=0 ./docker/run-tests.sh
RUN_UNIT=0 RUN_LIVE=1 RUN_STRESS=0 ./docker/run-tests.sh
RUN_GO_STRESS=0 ./docker/run-tests.sh
TEST_TRACE=0 ./docker/run-tests.sh
```

## Useful commands

View logs:

```bash
docker compose -f docker/docker-compose.yml logs -f nginx phoenix1 phoenix2 phoenix3 golang-public-1 golang-public-2 golang-admin redis-cluster-init
```

Stop:

```bash
docker compose -f docker/docker-compose.yml down
```

Stop and remove volumes:

```bash
docker compose -f docker/docker-compose.yml down -v
```

## Notes

- This is meant for local cluster testing, not production deployment.
- The services mount the local Elixir app source into the containers.
- Phoenix containers set `SKIP_DOTENV=1` so a local mounted `.env` does not
  override Docker-provided Redis/cluster settings.
- Health endpoints are intentionally internal-only in this stack. Use direct
  container-to-container access or the authenticated admin infra route instead
  of public Nginx URLs.
- The local Docker stack uses Cloudflare Turnstile test credentials, so the
  frontend test site key and Phoenix verification path work together without an
  insecure CAPTCHA bypass.
- The app services are cluster-only and seed from `redis-node-1:7000`, `redis-node-2:7001`, and `redis-node-3:7002`.
- If you are testing through a public hostname or tunnel, add that origin to the
  Phoenix `CORS_ORIGINS` values in the Compose file so websocket origin checks pass.
- Each Phoenix node gets its own `_build` and `deps` volume to avoid local build collisions.
- The Go services mount the local Go source and use cache volumes for modules/builds.
- The stack uses a named Docker network `pairline_cluster`, which makes it easier
  to plug in more Phoenix nodes or attach ad hoc debug containers.
- The network has a fixed subnet `172.30.0.0/24`, and each service has a static IP.
- The Redis Cluster nodes expose ports `7000` through `7005` on the host for direct cluster debugging.
- Nginx routes:
  - port `8080`: `/ws` to the Phoenix cluster
  - port `8081`: `/api/v1/moderation/*` and `/api/v1/webrtc/*` to the public Go worker pool
  - port `8081`: `/api/v1/admin/*` to the admin Go service
  - health endpoints are not proxied publicly; probe them from inside the Docker network or through the authenticated admin infra API
- The Phoenix upstream currently uses Nginx `least_conn` balancing.
- If you want to test more nodes, copy a Phoenix service block, change `PORT`,
  `hostname`, `NODE_NAME`, and `ipv4_address`, attach it to `pairline_cluster`,
  then update `CLUSTER_NODES` on every Phoenix node.
