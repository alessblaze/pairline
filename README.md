# Pairline

![Pairline Banner](./banner2.png)

Anonymous text and video chat app with moderation tooling. Built for massive scale with true concurrency.

## Stack

- [frontend/client/](./frontend/client): React 19 + Vite chat client
- [frontend/admin/](./frontend/admin): React 19 + Vite admin dashboard
- [backend/elixir/omegle_phoenix/](./backend/elixir/omegle_phoenix): Phoenix websocket and matchmaking service
- [backend/golang/](./backend/golang): moderation, admin, and TURN service
- Redis: session state and admin pub/sub
- PostgreSQL: reports, bans, and admin data

## Repo layout

```text
.
├── frontend/
│   ├── client/
│   └── admin/
├── backend/
│   ├── elixir/omegle_phoenix/
│   └── golang/
├── docker/
│   └── docker-compose.yml
├── SETUP.md
└── Vulnerabilities.md(Internal only.)
```

## Getting started

See [SETUP.md](./SETUP.md) for local setup, environment variables, and run commands.

## Service overview

Phoenix handles websocket sessions, matchmaking, and session IP tracking. The Go service handles report submission, admin APIs, ban persistence, and TURN credential generation. The frontend talks to both services: websocket traffic goes to Phoenix and HTTP moderation/admin traffic goes to Go.

## Notes

- Local app defaults are `5173` for the chat frontend, `5174` for the admin frontend, `8080` for Phoenix, and `8082` for the combined Go service.
- The Docker cluster stack exposes `8080` for Phoenix through Nginx and `8081` for Go through Nginx.
- `docker compose up -d` at the repo root now boots a 6-node local Valkey/Redis Cluster on ports `7000` through `7005` for migration work.
- `SHARED_SECRET` must match across the backend services.

## Why Pairline Scales

![Noah's Ark deployment philosophy](./noahsark.jpg)

... Any Problem?
Scale it up to half of the planet IDC. But be ethical.
Pairline leverages Elixir's battle-tested concurrency model and Go's lightweight goroutines to handle massive concurrent connections. Two of every connection, all day, every day.

## Additional docs

- [frontend/client/README.md](./frontend/client/README.md)
- [frontend/admin/README.md](./frontend/admin/README.md)
- [backend/elixir/omegle_phoenix/README.md](./backend/elixir/omegle_phoenix/README.md)
- [backend/golang/README.md](./backend/golang/README.md)
- [docker/README.md](./docker/README.md)

## Known Bugs
- Using Turn sometimes skips first entry while starting video streams.
- Could have additional bugs and issues as it is beta. Use at your own risk. Audit code by yourself.
- IPv6 banning isn't tested.

Keywords: omegle clone github, random video chat app, omegle alternative, open source video chat, webrtc video chat, omegle like, omegle clone, random video chat, omegle alternative, open source omegle, video chat app, random chat application, webrtc video chat, react video chat, omegle like app github, video chat github, omegle clone github, random video chat open source
