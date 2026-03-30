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

## Useful commands

```bash
mix phx.server
mix compile
mix test
```

## Main responsibilities

- websocket transport at `/ws`
- in-memory session lifecycle
- Redis-backed matchmaking queue
- room signaling and peer routing
- admin pub/sub consumption for emergency moderation actions
