# Pairline Architecture

This document describes the architecture of the Pairline anonymous chat platform. It covers the service topology, data flow, WebRTC/TURN infrastructure, matchmaking lifecycle, moderation pipeline, and deployment model.

For environment variable reference, see [ENVIRONMENT.md](./ENVIRONMENT.md). For TURN-specific details, see [TURNSERVER.md](./TURNSERVER.md). For auto-moderation, see [MODERATION.md](./MODERATION.md).

---

## High-Level Topology

```mermaid
graph TB
    subgraph Clients
        ChatClient["Chat Client<br/>(React 19 + Vite)"]
        AdminClient["Admin Dashboard<br/>(React 19 + Vite)"]
    end

    subgraph Ingress["Nginx Ingress"]
        Nginx["Nginx Reverse Proxy"]
    end

    subgraph Phoenix["Phoenix Cluster (Elixir/BEAM)"]
        PX1["phoenix-1"]
        PX2["phoenix-2"]
        PX3["phoenix-3"]
    end

    subgraph GoPublic["Go Public Service"]
        GP1["golang-public-1"]
        GP2["golang-public-2"]
    end

    subgraph GoAdmin["Go Admin Service"]
        GA["golang-admin"]
    end

    subgraph GoTurn["Go TURN Relay (host network)"]
        GT1["golang-turn-1"]
        GT2["golang-turn-2"]
    end

    subgraph Data["Data Layer"]
        Redis[("Valkey/Redis Cluster<br/>(6 nodes)")]
        Postgres[("PostgreSQL")]
    end

    subgraph Observability["Observability Stack"]
        OTEL["OpenTelemetry Collector"]
        Jaeger["Jaeger"]
        Prometheus["Prometheus"]
        Grafana["Grafana"]
    end

    ChatClient -->|"WS (Phoenix Channel)"| Nginx
    ChatClient -->|"HTTP + WS (Go)"| Nginx
    ChatClient -.->|"UDP/TCP TURN relay"| GT1 & GT2
    AdminClient -->|"HTTP API"| Nginx

    Nginx -->|":8080 → Phoenix"| PX1 & PX2 & PX3
    Nginx -->|":8081 → Go"| GP1 & GP2
    Nginx -->|":8081 → Go /admin"| GA

    PX1 & PX2 & PX3 -->|"session state, matchmaking"| Redis
    GP1 & GP2 -->|"session validation, bans"| Redis
    GA -->|"ban sync, config"| Redis
    GT1 -->|"gRPC :50051"| GP1
    GT2 -->|"gRPC :50052"| GP2

    GA -->|"reports, bans, accounts"| Postgres
    GP1 & GP2 -->|"reports, bans"| Postgres

    PX1 & PX2 & PX3 -->|traces| OTEL
    GP1 & GP2 & GA -->|traces + metrics| OTEL
    OTEL --> Jaeger
    OTEL --> Prometheus
    Prometheus --> Grafana
```

---

## Service Responsibilities

| Service | Owner | Responsibilities |
|---------|-------|-----------------|
| **Phoenix** | Elixir/BEAM | WebSocket sessions, matchmaking, session lifecycle, IP tracking, Turnstile verification, BEAM clustering |
| **Go Public** | Go | WebRTC signaling WS, TURN bootstrap, report submission, ban enforcement (Redis), gRPC TURN control plane |
| **Go Admin** | Go | Admin dashboard API, JWT auth, ban CRUD, report review, auto-moderation worker, infra health aggregation |
| **Go TURN** | Go (Pion) | TURN/STUN relay (UDP/TCP/TLS), allocation management, session validation via gRPC |
| **Redis/Valkey** | — | Session state, match state, ban cache, pub/sub coordination, queue shards |
| **PostgreSQL** | — | Reports, bans, admin accounts, banned words, bot definitions, auto-moderation settings |

---

## Data Flow: Chat Session Lifecycle

```mermaid
sequenceDiagram
    participant Browser
    participant Phoenix
    participant Redis
    participant GoPublic as Go Public
    participant GoTurn as Go TURN

    Note over Browser,Phoenix: 1. Session Establishment
    Browser->>Phoenix: WS connect (room:video)
    Phoenix->>Redis: Create session (data, token, locator, IP)
    Phoenix-->>Browser: session_id + session_token

    Note over Browser,Phoenix: 2. Matchmaking
    Browser->>Phoenix: "start_search" (interests)
    Phoenix->>Redis: Enqueue in shard queue
    Phoenix->>Redis: Match sweep (Lua atomics)
    Phoenix-->>Browser: "match" {peer_id, common_interests}

    Note over Browser,GoPublic: 3. WebRTC Setup
    Browser->>GoPublic: WS connect (/api/v1/webrtc/ws)
    Browser->>GoPublic: POST /api/v1/webrtc/turn
    GoPublic->>Redis: Validate session + match state
    GoPublic-->>Browser: ICE servers (STUN + TURN creds)

    Note over Browser,GoTurn: 4. Media Relay
    Browser->>GoTurn: TURN Allocate (UDP/TCP)
    GoTurn->>GoPublic: gRPC ValidateTurnUsername
    GoPublic->>Redis: Session/match/ban check
    GoPublic-->>GoTurn: allowed/denied
    GoTurn-->>Browser: Relay allocation

    Note over Browser,GoPublic: 5. Moderation
    Browser->>GoPublic: POST /api/v1/moderation/report
    GoPublic->>Postgres: Store report
    GoPublic->>GoPublic: Enqueue for auto-moderation
```

---

## TURN Control Plane

The TURN relay runs as a standalone process on host networking. Instead of reading Redis directly, it validates sessions through a gRPC control-plane API hosted by the Go public service.

```mermaid
graph LR
    subgraph "TURN Worker (host network)"
        TurnRelay["Pion TURN Server<br/>UDP :53478 / TCP"]
        TurnAuth["authHandler()"]
        TurnQuota["quotaHandler()"]
    end

    subgraph "Go Public Service"
        GRPC["gRPC Server<br/>:50051"]
        ValServer["turnControlValidationServer"]
        AuthLogic["turnservice.ValidateTURNUsername()"]
        QuotaLogic["turnservice.ReserveAllocationSlot()"]
    end

    subgraph "Redis Cluster"
        SessLocator["session:locator:{id}"]
        SessToken["session:{tag}:token:{id}"]
        SessData["session:{tag}:data:{id}"]
        MatchKey["match:{tag}:{id}"]
        BanKey["ban:{id} / ban:ip:{ip}"]
        AllocKey["turn:allocations:{id}"]
    end

    TurnAuth -->|"gRPC + shared secret"| GRPC
    TurnQuota -->|"gRPC ReserveAllocation"| GRPC
    GRPC --> ValServer
    ValServer --> AuthLogic
    ValServer --> QuotaLogic
    AuthLogic --> SessLocator & SessToken & SessData & MatchKey & BanKey
    QuotaLogic -->|"Lua INCR + PEXPIRE"| AllocKey
```

### TURN Auth Flow

1. Browser sends TURN `Allocate` request with username `{session_id}|{sha256(token)}`
2. Pion calls `authHandler` → gRPC `ValidateTurnUsername` to Go public service
3. Go public validates: session exists → token matches → session active → not banned → currently matched → peer reciprocates
4. On success, Pion calls `quotaHandler` → gRPC `ReserveAllocation` with per-session limit
5. Allocation counter is managed via atomic Lua script with 24h safety TTL
6. On allocation teardown, `OnAllocationDeleted` callback releases the slot

---

## Matchmaking Architecture

```mermaid
graph TB
    subgraph "Client Request"
        Search["start_search<br/>(mode, interests)"]
    end

    subgraph "Queue Tiers"
        Exact["Exact Interest Queue<br/>(per interest combo)"]
        Relaxed["Relaxed Tier<br/>(sharded, any-interest)"]
        Random["Random Tier<br/>(sharded, no-interest)"]
    end

    subgraph "Matching Engine"
        Leader["Matchmaker Leader<br/>(Redis lease TTL)"]
        LuaPair["Lua Atomic Pair Script"]
        Stream["Cross-Node Event Stream<br/>(match:event:stream)"]
    end

    subgraph "Result"
        Matched["Match Notification<br/>(both peers)"]
    end

    Search -->|"immediate"| Exact
    Search -->|"after RELAXED_WAIT_MS"| Relaxed
    Search -->|"after OVERFLOW_WAIT_MS"| Random

    Exact & Relaxed & Random -->|"sweep"| Leader
    Leader -->|"atomic pair"| LuaPair
    LuaPair -->|"coordinate"| Stream
    Stream --> Matched

    style Exact fill:#2d6a4f,color:#fff
    style Relaxed fill:#40916c,color:#fff
    style Random fill:#52b788,color:#fff
```

### Queue Shard Design

- Queues are sharded by mode (lobby/text/video) across `MATCH_SHARD_COUNT` Redis hash slots
- Interest-based matching checks exact interest buckets first
- Sessions fall through tiers over time: exact → relaxed → random
- Cross-shard matchmaking uses a normalized queue shard so sessions across the cluster can discover each other
- Match pairing is atomic via Lua scripts to prevent double-matching

---

## Moderation Pipeline

```mermaid
graph TB
    subgraph "User Flow"
        Report["User submits report<br/>POST /api/v1/moderation/report"]
    end

    subgraph "Go Public"
        Validate["Validate session + peer<br/>(Redis lookup)"]
        Store["Store report<br/>(Postgres)"]
        Enqueue["Wake auto-mod worker"]
    end

    subgraph "Auto-Moderation Worker (Go Admin)"
        Poll["Poll pending reports<br/>SELECT FOR UPDATE SKIP LOCKED"]
        Extract["Extract evidence<br/>(reported + reporter)"]
        LLM["LLM Safety Assessment<br/>(OpenAI-compatible API)"]
        Decide["determineDecision()<br/>approve / reject / escalate"]
        CounterBan["Counter-ban abusive<br/>reporters (7-day silent)"]
    end

    subgraph "Admin Dashboard"
        Review["Human review<br/>(escalated reports)"]
        BanAction["Ban / unban actions"]
    end

    subgraph "Enforcement"
        RedisBan["Redis ban cache<br/>(real-time)"]
        PGBan["Postgres ban record<br/>(durable)"]
        PhoenixSync["Phoenix admin stream<br/>(fan-out disconnect)"]
    end

    Report --> Validate --> Store --> Enqueue
    Enqueue --> Poll --> Extract --> LLM --> Decide
    Decide -->|"auto-reject"| RedisBan & PGBan
    Decide -->|"reporter abusive"| CounterBan --> RedisBan & PGBan
    Decide -->|"escalate"| Review
    Review --> BanAction --> RedisBan & PGBan
    RedisBan --> PhoenixSync

    style LLM fill:#7b2cbf,color:#fff
```

---

## Bot System

Bots are managed by Phoenix and run as supervised GenServer processes under `OmeglePhoenix.Bots.Supervisor`. They are assigned to waiting sessions based on admin-configured definitions stored in Redis. There are two bot types:

| Type | Worker | How it generates replies |
|------|--------|------------------------|
| **Engagement** | `ScriptWorker` | JSON-scripted messages with trigger/regex matching, opening/closing/fallback messages |
| **AI** | `AIWorker` | LLM-backed via LangChain (`ChatOpenAI`), per-definition API endpoint/model/system prompt |

```mermaid
graph TB
    subgraph "Matchmaker"
        Search["Session enters queue"]
    end

    subgraph "Bot Assignment"
        MaybeAssign["Bots.maybe_assign_waiting_session()"]
        Snapshot["Load bots:definitions:snapshot<br/>(Redis JSON)"]
        Filter["Filter by mode, active, rollout %"]
        Prioritize["Weighted shuffle by<br/>traffic_weight + bot_type priority"]
        Reserve["Reserve slot<br/>(Lua: global + per-definition counters)"]
    end

    subgraph "Bot Workers (DynamicSupervisor)"
        AIW["AIWorker (GenServer)<br/>LangChain → OpenAI-compatible API"]
        SW["ScriptWorker (GenServer)<br/>JSON trigger/response scripts"]
    end

    subgraph "Session Lifecycle"
        Pair["pair_with_human()<br/>(SessionLock + SessionManager)"]
        Route["Router.notify_match()"]
        Chat["Message exchange via Router"]
        End["Disconnect on idle / TTL / max messages"]
        Release["Release definition slot<br/>(Lua DECR + DEL)"]
    end

    Search --> MaybeAssign --> Snapshot --> Filter --> Prioritize --> Reserve
    Reserve -->|"engagement"| SW
    Reserve -->|"ai"| AIW
    SW & AIW --> Pair --> Route --> Chat --> End --> Release
```

### Bot Capacity Management

Slot reservation uses a two-key Lua script with `{bot-active-runs}` hash tag:
- `bots:active_runs:{bot-active-runs}:global` — global concurrent run counter
- `bots:active_runs:{bot-active-runs}:definition:{id}` — per-definition counter

Both keys have a safety TTL derived from `session_ttl_seconds + 60s`. The hash tag ensures both keys land on the same Redis cluster slot for atomic Lua execution.

### Bot Admin Management

Bot definitions are managed through the admin dashboard (Go admin API → Postgres) and published as a JSON snapshot to Redis (`bots:definitions:snapshot`). The Go admin also serves a CRUD API for bot definitions, AI config, and engagement scripts.

---

## Cluster-Aware Session Router

The `OmeglePhoenix.Router` module handles cross-node message delivery in a multi-node Phoenix cluster. It uses a combination of local ETS lookup and Redis-backed owner records.

```mermaid
graph LR
    subgraph "Phoenix Node A"
        ETS_A["ETS :omegle_phoenix_router_owners<br/>(session_id → pid)"]
        PubSub_A["Phoenix.PubSub"]
    end

    subgraph "Phoenix Node B"
        ETS_B["ETS :omegle_phoenix_router_owners"]
        PubSub_B["Phoenix.PubSub"]
    end

    subgraph "Redis"
        OwnerKey["session:{tag}:owner:{id}<br/>= 'nodeA|token'<br/>TTL: 30s"]
    end

    Router["Router.send_message(session_id, msg)"]

    Router -->|"1. Check local ETS"| ETS_A
    Router -->|"2. Fallback: GET owner key"| OwnerKey
    Router -->|"3. Remote: PubSub broadcast"| PubSub_A
    PubSub_A -.->|"distributed Erlang"| PubSub_B
```

**Delivery strategy:**
1. Check local ETS for a live `pid` — if found, deliver directly via `send/2`
2. If not local, read the Redis owner key to find which node owns the session
3. If the owner is a remote connected node, broadcast via `Phoenix.PubSub`
4. Owner records use compare-and-delete Lua scripts to prevent stale cleanup races

---

## Real-Time Banned-Word Filter

`MessageModeration` is a GenServer that periodically syncs the banned-word set from Redis into `persistent_term` for zero-cost reads on the hot path.

- Source: `moderation:banned_words` (Redis SET), managed by admin dashboard
- Toggle: `moderation:banned_words:enabled` (Redis key)
- Refresh: every 30 seconds
- Matching: tokenized n-gram phrase matching (multi-word phrases supported)
- Used by `RoomChannel` to silently block messages containing banned phrases

---

## Reaper (Orphan Cleanup)

The `Reaper` is a leader-elected GenServer that periodically scans for stale state:

1. **Orphaned sessions** — entries in `sessions:active` (Redis SET) whose session data no longer exists
2. **Stale queue entries** — sessions in matchmaking queues that are no longer in `:waiting` status

Leader election uses `SET NX PX` on `reaper:leader` with a 5s TTL, renewed in a background process. Only one node reaps at a time across the cluster.

---

## Elixir OTP Supervision Tree

```mermaid
graph TB
    App["OmeglePhoenix.Application"]
    App --> Redis["Redis Cluster Client"]
    App --> PubSub["Phoenix.PubSub"]
    App --> Router["Router (GenServer)"]
    App --> Matchmaker["Matchmaker (GenServer)"]
    App --> SessionMgr["SessionManager"]
    App --> Reaper["Reaper (GenServer)"]
    App --> MsgMod["MessageModeration (GenServer)"]
    App --> BotSup["Bots.Supervisor (DynamicSupervisor)"]
    App --> TaskSup["TaskSupervisor"]
    App --> Cluster["ClusterConnector (GenServer)"]
    App --> Endpoint["Phoenix.Endpoint"]
    App --> Telemetry["OtlpMetrics + Metrics"]

    BotSup --> AIW1["AIWorker"]
    BotSup --> AIW2["AIWorker"]
    BotSup --> SW1["ScriptWorker"]
    TaskSup --> LLMTask["LLM generation tasks"]

    Endpoint --> UserSocket["UserSocket"]
    UserSocket --> RoomChannel["RoomChannel<br/>(room:lobby, room:text, room:video)"]
```

---

## Frontend Architecture

### Chat Client (`frontend/client`)

```mermaid
graph TB
    subgraph "Entry Points"
        App["App.tsx"]
        Landing["LandingPage"]
        Entry["EntryModal<br/>(mode, interests, Turnstile)"]
    end

    subgraph "Chat Modes"
        Text["TextChat"]
        Video["VideoChat"]
        VideoOff["VideoDisabled"]
    end

    subgraph "Core Hooks"
        UseText["useTextChat<br/>(Phoenix WS only)"]
        UseVideo["useVideoChat<br/>(Phoenix WS + Go WS)"]
        UseTheme["useTheme"]
        UseHealth["useNetworkHealth"]
    end

    subgraph "Services"
        WS["WebSocketClient<br/>(Phoenix Channel)"]
        GoWS["Native WebSocket<br/>(Go signaling)"]
        TURN["TURN fetch + prefetch"]
    end

    subgraph "Shared UI"
        Report["ReportDialog"]
        Theme["ThemeToggle"]
        Error["ErrorBoundary"]
        Unavail["ServiceUnavailable"]
    end

    App --> Landing --> Entry
    Entry -->|text| Text
    Entry -->|video| Video

    Text --> UseText --> WS
    Video --> UseVideo --> WS & GoWS & TURN
    Video --> Report
```

### Admin Dashboard (`frontend/admin`)

The admin dashboard is a single-page React app (`AdminPanelRuntime.tsx`, 250KB+) that communicates exclusively with the Go admin API via JWT-authenticated REST calls.

**Tabs / Features:**
- Reports queue with auto-moderation verdicts
- Ban management (session + IP bans, temporary/permanent)
- Banned words CRUD with toggle
- Bot definitions management (engagement scripts + AI config)
- Infrastructure health dashboard (Phoenix + Go + TURN node status)
- Admin user management

---

## Go Binary Capabilities

The Go service compiles into multiple binaries with different capability sets:

```mermaid
graph LR
    subgraph "go run ."
        Combined["Combined Server<br/>(all capabilities)"]
    end

    subgraph "cmd/public"
        Public["Public Server"]
    end

    subgraph "cmd/admin"
        Admin["Admin Server"]
    end

    subgraph "cmd/turn"
        Turn["TURN Relay"]
    end

    Combined ---|"Admin API ✓<br/>Moderation API ✓<br/>Signaling WS ✓<br/>TURN Bootstrap ✓<br/>TURN Control API ✓"| Combined

    Public ---|"Moderation API ✓<br/>Signaling WS ✓<br/>TURN Bootstrap ✓<br/>TURN Control API ✓"| Public

    Admin ---|"Admin API ✓<br/>(auto-mod worker)"| Admin

    Turn ---|"Pion TURN relay<br/>gRPC or direct Redis"| Turn
```

| Capability | `go run .` | `cmd/public` | `cmd/admin` | `cmd/turn` |
|-----------|:---:|:---:|:---:|:---:|
| Admin API | ✓ | | ✓ | |
| Moderation API | ✓ | ✓ | | |
| Signaling WS | ✓ | ✓ | | |
| TURN Bootstrap | ✓ | ✓ | | |
| TURN Control gRPC | ✓ | ✓ | | |
| TURN Relay | | | | ✓ |
| Auto-mod Worker | ✓ | ✓ | ✓ | |
| Ban Sync Loop | ✓ | ✓ | ✓ | |

---

## Redis Key Topology

All session-scoped keys use `{mode:shard}` hash tags to ensure co-location on the same Redis cluster slot.

| Key Pattern | Owner | TTL | Purpose |
|-------------|-------|-----|---------|
| `session:locator:{id}` | Phoenix | session TTL | Maps session ID → `mode\|shard` route |
| `session:{mode:shard}:data:{id}` | Phoenix | session TTL | Session metadata (interests, config) |
| `session:{mode:shard}:token:{id}` | Phoenix | session TTL | SHA-256 of session token |
| `session:{mode:shard}:ip:{id}` | Phoenix | session TTL | Client IP for ban checks |
| `session:{mode:shard}:owner:{id}` | Phoenix | 30s lease | Which Phoenix node owns this session |
| `match:{mode:shard}:{id}` | Phoenix | — | Current match peer ID |
| `ban:{session_id}` | Go | ban duration | Active session ban |
| `ban:ip:{address}` | Go | ban duration | Active IP ban |
| `bans:index` | Go | — | Set of all active ban keys |
| `turn:allocations:{session_id}` | Go | 24h safety | TURN allocation counter per session |
| `webrtc:{mode:shard}:ready:{id}` | Phoenix | — | WebRTC readiness flag |
| `webrtc:turn:cache:cloudflare:{user}` | Go | 10min | Cached Cloudflare TURN credentials |

---

## Docker Cluster Layout

### Default Local Dev (`docker-compose.yml`)

```mermaid
graph TB
    subgraph "Docker Bridge Network (172.30.0.0/16)"
        Nginx["nginx<br/>:8080 → Phoenix<br/>:8081 → Go"]
        PX1["phoenix-1<br/>:8080"]
        PX2["phoenix-2<br/>:8080"]
        PX3["phoenix-3<br/>:8080"]
        GP1["golang-public-1<br/>:8082"]
        GP2["golang-public-2<br/>:8082"]
        GA["golang-admin<br/>:8082"]
        PG["postgres<br/>:5432"]
        R1["redis-1<br/>:7000"]
        R2["redis-2<br/>:7001"]
        R3["redis-3<br/>:7002"]
        R4["redis-4<br/>:7003"]
        R5["redis-5<br/>:7004"]
        R6["redis-6<br/>:7005"]
        OTEL["otel-collector"]
        Jaeger["jaeger<br/>:16686"]
        Prom["prometheus<br/>:9090"]
        Graf["grafana<br/>:3000"]
    end

    subgraph "Host Network"
        GT1["golang-turn-1<br/>UDP :53478"]
        GT2["golang-turn-2<br/>UDP :53479"]
    end

    Nginx --> PX1 & PX2 & PX3
    Nginx --> GP1 & GP2 & GA
    GT1 -->|"gRPC :50051"| GP1
    GT2 -->|"gRPC :50052"| GP2
```

### Elixir Cluster Compose (`elixir-cluster-compose.yml`)

Production-like layout (gitignored, contains secrets). Same topology as default but with:
- Real CORS origins and secrets
- `TURN_PUBLIC_IP` set to the host's LAN IP
- BEAM node clustering enabled across Phoenix instances
- Host-specific Redis passwords

---

## Network Ports (Default Dev)

| Port | Service | Protocol | Exposed |
|------|---------|----------|---------|
| 5173 | Chat Frontend | HTTP | localhost |
| 5174 | Admin Frontend | HTTP | localhost |
| 8080 | Phoenix (direct) / Nginx→Phoenix | HTTP+WS | localhost |
| 8081 | Nginx→Go | HTTP | localhost |
| 8082 | Go Combined (direct) | HTTP+WS | localhost |
| 7000–7005 | Redis Cluster | TCP | localhost |
| 5432 | PostgreSQL | TCP | localhost |
| 50051–50052 | TURN Control gRPC | TCP | internal |
| 53478–53479 | TURN Relay | UDP+TCP | host network |
| 16686 | Jaeger UI | HTTP | localhost |
| 9090 | Prometheus | HTTP | localhost |
| 3000 | Grafana | HTTP | localhost |
| 4317–4318 | OTEL Collector | gRPC+HTTP | internal |

---

## Cross-Service Auth

```mermaid
graph LR
    subgraph "Shared Secrets"
        SS["SHARED_SECRET<br/>(Phoenix ↔ Go)"]
        GRPCS["TURN_CONTROL_GRPC_SHARED_SECRET<br/>(Go Public ↔ Go TURN)"]
        JWT["JWT_SECRET<br/>(Go Admin)"]
    end

    Phoenix -->|"HMAC session verify"| SS
    GoPublic -->|"HMAC session verify"| SS
    GoTurn -->|"gRPC metadata header"| GRPCS
    GoPublic -->|"gRPC interceptor"| GRPCS
    AdminDashboard -->|"Bearer token"| JWT
    GoAdmin -->|"JWT sign/verify"| JWT
```

| Boundary | Mechanism | Notes |
|----------|-----------|-------|
| Phoenix ↔ Go | `SHARED_SECRET` HMAC | Used in session token verification |
| Go Public ↔ Go TURN | gRPC metadata + constant-time compare | `x-pairline-turn-control-auth` header |
| Admin Dashboard ↔ Go Admin | JWT (access + refresh) | CSRF double-submit cookie + origin check |
| Browser → TURN | TURN long-term credential | Username = `{session_id}\|{sha256(token)}`, password = `TURN_STATIC_AUTH_SECRET` |
