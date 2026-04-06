# Admin Frontend

Standalone React 19 + Vite moderation dashboard for Pairline.

## Local development

```bash
npm install
npm run dev
```

The app runs on `http://localhost:5174`.

## Environment

Copy `.env.example` to `.env` and review:

```bash
VITE_API_URL=http://localhost:8082
VITE_ADMIN_BASE_PATH=/
```

`VITE_API_URL` points at the Go admin API.

## Useful commands

```bash
npm run dev
npm run build
npm run lint
```
