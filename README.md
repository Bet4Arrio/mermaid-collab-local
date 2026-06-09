# mermaid-collab

A real-time collaborative [Mermaid](https://mermaid.js.org/) diagram editor.
Multiple people edit the same diagram at once; every keystroke syncs instantly
over WebSockets using the [Yjs](https://yjs.dev/) CRDT protocol, with a live SVG
preview rendered as you type.

**Single-binary deploy:** Go compiles the backend *and* serves the Vite-built
React SPA as static files. There is no separate frontend server in production.

---

## How it works

```
Browser (CodeMirror 6) ⇄ Yjs doc ⇄ y-websocket
                                        │
                                        ▼
                          Go relay  /ws/:roomId   (opaque update log)
                                        │
                                        ▼
                          PostgreSQL  rooms.yjs_state (BYTEA)
```

The non-obvious core: **the Go server does not run a Yjs/CRDT engine.** It keeps
an ordered, *opaque* append-log of Yjs update frames per room and only stores,
relays, and compacts them. Clients run the real Yjs engine and converge by
replaying the log. WebSocket frames are binary and relayed verbatim — the server
never JSON-encodes or inspects their contents (doing so would break the sync
protocol).

- **Persistence** — the whole log is serialized into the `rooms.yjs_state` blob,
  written only when the last client leaves a room and on graceful shutdown.
  On reconnect the blob is replayed to the first joiner. Never per-update.
- **Compaction** — to keep the log from growing unbounded, a periodic ticker asks
  one *trusted* client (connected long enough, never had a frame dropped) for a
  full snapshot and replaces the log up to that point. No server-side CRDT merge,
  so no risk of corruption.

## Tech stack

| Layer    | Tech                                                          |
|----------|---------------------------------------------------------------|
| Backend  | Go 1.22, [Fiber v2](https://gofiber.io/), `database/sql` + `lib/pq` (no ORM) |
| Frontend | React 18, TypeScript (strict), Vite 5                          |
| Editor   | CodeMirror 6 + `y-codemirror.next`, shared cursors via awareness |
| Sync     | Yjs + y-websocket (binary CRDT relay)                         |
| Preview  | Mermaid.js 11 (debounced render, inline parse errors)          |
| Storage  | PostgreSQL 16 (source of truth) · Redis 7 (connected, not yet wired in) |

## Project layout

```
mermaid-collab/
├── backend/                 # Go 1.22 / Fiber v2
│   ├── main.go              # env, routes, WS origin allowlist, graceful shutdown
│   ├── handlers/
│   │   ├── ws.go            # WebSocket hub — Yjs relay + compaction protocol
│   │   └── rooms.go         # REST CRUD under /api/rooms
│   ├── models/room.go       # database/sql queries
│   ├── db/db.go             # pool + idempotent schema
│   └── frontend/dist/       # ← Vite build output lands here (gitignored)
├── frontend/                # React 18 + Vite + TS
│   └── src/
│       ├── App.tsx          # Lobby + RoomView (room id from URL hash)
│       ├── lib/yjs.ts       # collab provider factory
│       └── components/      # Editor.tsx, Preview.tsx, Presence.tsx
├── migrations/001_init.sql
├── Makefile
├── docker-compose.yml       # postgres:16 + redis:7
└── Dockerfile               # 3-stage: SPA → static Go binary → alpine runtime
```

## Getting started

### Prerequisites

- Go 1.22+
- Node.js 18+ (for the frontend build)
- Docker (for Postgres + Redis), or your own Postgres 16

### Setup

```bash
cp .env.example .env       # adjust if needed
make install               # npm install in frontend/
make docker-up             # start postgres:16 + redis:7
make dev                   # backend :3000 + Vite dev server :5173
```

Then open **http://localhost:5173** — Vite proxies `/api` and `/ws` to the Go
backend, so you always hit the app through Vite in development.

### Production build (single binary)

```bash
make build                 # vite build → backend/frontend/dist, then go build
make run                   # serves API + SPA from one process on :3000
```

`make run` must run from `backend/` (the Makefile handles this) so the binary
finds `./frontend/dist`.

## Make targets

| Target          | What it does                                                  |
|-----------------|---------------------------------------------------------------|
| `make install`  | `npm install` in `frontend/`                                  |
| `make docker-up`| `docker compose up -d` (postgres + redis)                     |
| `make dev`      | Backend + Vite dev server concurrently (Ctrl-C stops both)    |
| `make build`    | Build the SPA, then compile the single Go binary             |
| `make run`      | Run the compiled binary                                       |
| `make migrate`  | Apply `migrations/*.sql` via `migrate.sh` (uses `DB_URL`)     |
| `make clean`    | Remove `backend/frontend/dist` and the binary                |

> The backend also auto-migrates on startup; `make migrate` is for running
> migrations independently. Keep every migration idempotent.

## Configuration

Config is read from the environment (see `.env.example`):

| Variable          | Default                                                                   | Used by |
|-------------------|---------------------------------------------------------------------------|---------|
| `PORT`            | `3000`                                                                     | Go      |
| `DB_URL`          | `postgres://postgres:postgres@localhost:5432/mermaid_collab?sslmode=disable` | Go   |
| `REDIS_URL`       | `redis://localhost:6379`                                                   | Go      |
| `ENV`             | `development` (enables Vite-origin CORS + dev WS origin)                   | Go      |
| `ALLOWED_ORIGINS` | *(empty)* — comma-separated extra WS origins allowed for the upgrade      | Go      |

Browser-visible Vite variables must be prefixed with `VITE_`.

## API

| Method | Route             | Description                          |
|--------|-------------------|--------------------------------------|
| GET    | `/api/rooms`      | List rooms                           |
| POST   | `/api/rooms`      | Create a room                        |
| GET    | `/api/rooms/:id`  | Get a room                           |
| DELETE | `/api/rooms/:id`  | Delete a room                        |
| WS     | `/ws/:roomId`     | Yjs relay (binary). Room must exist  |

Route order in `main.go` is load-bearing: `/ws`, then `/api`, **then** the static
file server and the `*` SPA fallback to `index.html` (which must stay last).

## Security notes

The app is intentionally **anonymous/open — there is no authentication**. The
relay still enforces:

- **WebSocket Origin allowlist** (anti-CSWSH) on the `/ws` upgrade.
- Rooms are **never created over WebSocket** — `/ws/:roomId` requires a valid UUID
  that already exists in the DB, else closes with `1008`.
- Frame size limit (1 MiB), per-room log ceiling (32 MiB), per-room connection cap.
- Ping/pong keepalive reaps dead connections; slow clients are dropped.
- REST hardening: UUID validation, title length cap, body limit, parameterized SQL.

## Status / known gaps

- **No test suite** yet (no `*_test.go`, no frontend test runner).
- **Redis is connected but unused** — the multi-instance pub/sub fan-out and
  presence TTL are not yet wired in, so the relay is effectively single-instance.

---

See [`CLAUDE.md`](./CLAUDE.md) and [`../CLAUDE.md`](../CLAUDE.md) for the full
architecture and design rules.
