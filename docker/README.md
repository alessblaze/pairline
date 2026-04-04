# Docker Cluster Test Setup

This folder contains a local end-to-end Docker Compose stack for testing:

- a 2-node Elixir/Phoenix cluster
- a 2-node public Go backend
- a separate admin Go backend
- Redis
- Postgres
- Nginx ingress ports split by backend

## What it starts

- `nginx` on:
  - `http://localhost:8080` for Phoenix
  - `http://localhost:8081` for Go
- `redis`
- `postgres`
- `phoenix1`
- `phoenix2`
- `golang-public-1`
- `golang-public-2`
- `golang-admin`

Nginx is the only exposed ingress layer. Phoenix and Go stay internal to the
Docker network, while host access is split cleanly by port.

Both Phoenix nodes share:

- the same Redis instance
- the same Redis password: `pairline-dev-redis-password`
- the same Erlang cookie
- the same cluster node list
- the same explicit Docker network: `pairline_cluster`

Static IP assignments on `172.30.0.0/24`:

- `nginx` -> `172.30.0.10`
- `phoenix1` -> `172.30.0.11`
- `phoenix2` -> `172.30.0.12`
- `redis` -> `172.30.0.20`
- `postgres` -> `172.30.0.30`
- `golang-public-1` -> `172.30.0.40`
- `golang-public-2` -> `172.30.0.42`
- `golang-admin` -> `172.30.0.41`

Internal app ports:

- `phoenix1` -> `8080`
- `phoenix2` -> `8081`
- `golang-public-1` -> `8081`
- `golang-public-2` -> `8081`
- `golang-admin` -> `8082`
- `nginx` -> exposed on host `8080` and `8081`

All Go services share the same Redis and Postgres backing services.

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

Phoenix health through Nginx:

```bash
curl http://localhost:8080/api/health
```

Go health through Nginx:

```bash
curl http://localhost:8081/health
```

You should see for `/api/health`:

- `node` set to either `phoenix1@phoenix1` or `phoenix2@phoenix2`
- `connected_nodes` listing the peer Phoenix node
- shared-state counters and `active_sessions`

## Useful commands

View logs:

```bash
docker compose -f docker/docker-compose.yml logs -f nginx phoenix1 phoenix2 golang-public-1 golang-public-2 golang-admin
```

Stop:

```bash
docker compose -f docker/docker-compose.yml down
```

Stop and remove volumes:

```bash
docker compose -f docker/docker-compose.yml down -v
```

Inspect a specific Phoenix node directly:

```bash
docker compose -f docker/docker-compose.yml exec phoenix1 curl -s http://localhost:8080/api/health
docker compose -f docker/docker-compose.yml exec phoenix2 curl -s http://localhost:8081/api/health
```

## Notes

- This is meant for local cluster testing, not production deployment.
- The services mount the local Elixir app source into the containers.
- Phoenix containers set `SKIP_DOTENV=1` so a local mounted `.env` does not
  override Docker-provided Redis/cluster settings.
- If you are testing through a public hostname or tunnel, add that origin to the
  Phoenix `CORS_ORIGINS` values in the Compose file so websocket origin checks pass.
- Each Phoenix node gets its own `_build` and `deps` volume to avoid local build collisions.
- The Go services mount the local Go source and use cache volumes for modules/builds.
- The stack uses a named Docker network `pairline_cluster`, which makes it easier
  to plug in more Phoenix nodes or attach ad hoc debug containers.
- The network has a fixed subnet `172.30.0.0/24`, and each service has a static IP.
- Nginx routes:
  - port `8080`: `/ws` and `/api/health` to the Phoenix cluster
  - port `8081`: `/api/v1/moderation/*`, `/api/v1/webrtc/*`, and `/health` to the public Go worker pool
  - port `8081`: `/api/v1/admin/*` to the admin Go service
- The Phoenix upstream currently uses Nginx `least_conn` balancing.
- If you want to test more nodes, copy a Phoenix service block, change `PORT`,
  `hostname`, `NODE_NAME`, and `ipv4_address`, attach it to `pairline_cluster`,
  then update `CLUSTER_NODES` on every Phoenix node.
