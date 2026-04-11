# Frontend

React 19 + Vite client for anonymous text/video chat and reporting.

## Local development

Use the shared setup guide in [../../SETUP.md](../../SETUP.md) for the full stack, then run:

```bash
npm install
npm run dev
```

The app runs on `http://localhost:5173`.

## Environment

Copy `.env.example` to `.env` and review:

```bash
VITE_API_URL=http://localhost:8082
VITE_WS_URL=ws://localhost:8080/ws
```

`VITE_API_URL` points at the Go service. `VITE_WS_URL` points at Phoenix.

## Useful commands

```bash
npm run dev
npm run build
npm run lint
```

## Main areas

- `src/components/`: chat UI and report dialog
- `src/hooks/`: chat and theme hooks
- `src/services/websocket.ts`: Phoenix websocket client
- `src/types/`: shared frontend types
