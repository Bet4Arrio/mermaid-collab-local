# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> A second, more exhaustive conventions/architecture doc lives one directory up at
> `../CLAUDE.md` (the "design rules" — static-serving order, Yjs protocol bytes,
> security invariants, "what not to do"). Read it too; this file is the practical
> command + big-picture overview and notes where the running code differs from those rules.

---

## What this is

A real-time collaborative Mermaid diagram editor. **Single-binary deploy:** Go
compiles the backend *and* serves the Vite-built React SPA as static files from
`./frontend/dist` (relative to the binary's CWD). There is no separate frontend
server in production.

The non-obvious core: **the Go server does not run a Yjs/CRDT engine.** It keeps
an ordered, *opaque* append-log of Yjs update frames per room and only stores,
relays, and compacts them. Clients run the real Yjs engine and converge by
replaying the log. See "WebSocket relay" below — this is the single most
important thing to understand before touching `handlers/ws.go`.

## Commands

```bash
make install      # cd frontend && npm install (Go deps resolve on build)
make docker-up    # docker compose up -d  (postgres:16 + redis:7) — needed for dev
make dev          # backend :3000 + Vite dev server :5173 concurrently (Ctrl-C stops both)
make build        # npm run build → backend/frontend/dist, then go build -o backend/mermaid-collab
make run          # cd backend && ./mermaid-collab  (must run from backend/ so ./frontend/dist resolves)
make migrate      # ./migrate.sh — apply migrations/*.sql via psql using DB_URL
make clean        # rm backend/frontend/dist + the binary
```

Single-package operations (when not using the Makefile):
- Frontend build = `tsc -b && vite build` (typecheck is part of the build).
- Backend: `cd backend && go build ./...` / `go vet ./...` / `go run .`.

**Tests:** there is currently **no test suite** (no `*_test.go`, no frontend test
runner configured). Don't invent test commands; if asked to add tests, wire the
runner up first.

Dev flow: `make docker-up` once, then `make dev`. Vite (`:5173`) proxies `/api`
and `/ws` to Go (`:3000`), so you always hit the app at `http://localhost:5173`.

## Architecture

### Backend (Go 1.22 / Fiber v2) — `backend/`
- `main.go` — env loading, Postgres connect + auto-migrate, optional Redis,
  Fiber app, route order, WS origin allowlist (`originAllowed`), graceful shutdown.
- `handlers/ws.go` — the WebSocket hub and the entire relay/compaction protocol.
- `handlers/rooms.go` — REST CRUD under `/api/rooms` (list/create/get/delete).
- `models/room.go` — `database/sql` queries (no ORM).
- `db/db.go` — pool + idempotent schema (`CREATE TABLE IF NOT EXISTS rooms`).

Route order in `main.go` is load-bearing: `/ws` middleware + `/ws/:roomId`, then
`/api`, **then** `app.Static("/")` and the `*` SPA-fallback to `index.html`.
Static + fallback must stay last or they shadow the API.

### WebSocket relay (`handlers/ws.go`) — the heart of the system
- One goroutine per `Room` (`run`) owns all client-map mutation; everything else
  takes the `RWMutex`. Three channels: `register`, `unregister`, `broadcast`.
  `broadcast` carries an `envelope{sender, data}` so a frame is never echoed to
  its origin.
- Frames are **binary only**. The server reads the y-websocket type prefix
  (`classifySync`) purely to drive the sync handshake — `syncStep1` → reply with
  full log via `sendState`; `syncStep2`/`syncUpdate` → `appendUpdate`. Bytes are
  relayed verbatim; **never JSON-encode/decode them**. lib0 varint helpers
  (`writeVarUint`, `readVarUint8Array`, etc.) live at the bottom of the file.
- **Persistence**: the whole log is serialized (`encodeLog`/`decodeLog`) into the
  `rooms.yjs_state` BYTEA blob, written **only when the last client leaves**
  (`persistRoom`) and on shutdown (`PersistAll`). Never write per-update. On
  reconnect the blob is loaded and replayed to the first joiner.
- **Compaction without server-side merge**: a `compactInterval` ticker, once the
  log exceeds `compactThreshold` entries, asks **one trusted client** for a full
  snapshot (`encodeSyncStep1` with empty state vector). "Trusted" = connected
  past `compactMinAge` and never `lossy` (never had a frame dropped). The reply,
  flagged by `Client.awaitingFull`, replaces the log up to `compactMark` and
  re-appends the tail (duplicates are idempotent on replay → lossless). Empty
  snapshots are rejected. A fresh joiner's intro reply is appended, never trusted.
- Safety limits are constants near the top: `maxMessageBytes` (1 MiB read limit),
  `maxRoomBytes` (32 MiB log ceiling), `maxClientsPerRoom` (50), ping/pong
  keepalive (`pingInterval`/`pongWait`), per-write deadline. Slow clients get
  flagged `lossy` and dropped. The WS goroutine has its own `recover()` (Fiber's
  recover middleware does not cover it).

### Frontend (React 18 + Vite + TS strict) — `frontend/src/`
- `lib/yjs.ts` — `createCollabProvider(roomId)`; derives the WS URL from
  `window.location.host` (never hardcode localhost). y-websocket appends the
  room name, so base is `…/ws` and `roomId` is the room arg.
- `App.tsx` — Lobby (REST room list/create/delete) + RoomView. Room id comes from
  the URL **hash** (`/#<roomId>`); no React Router. The collab provider and
  `Y.UndoManager` are created **once per room in refs** (not state, not in a
  `useEffect`); `RoomView` is keyed on `roomId` so switching rooms re-mounts.
- `components/Editor.tsx` — CodeMirror 6 bound to `Y.Text('mermaid')` via
  `y-codemirror.next` (`yCollab`), shared cursors through awareness.
- `components/Preview.tsx` — Mermaid render, **debounced 300ms**, `mermaid.parse`
  before `mermaid.render`, parse errors shown inline. `mermaid.initialize({
  startOnLoad: false })` is called once in `main.tsx`.

### Data flow at a glance
Browser CodeMirror ⇄ Yjs doc ⇄ y-websocket ⇄ Go relay (`/ws/:roomId`, opaque
log) ⇄ Postgres `yjs_state` blob. Mermaid preview renders off the same Yjs text.

## Database & migrations
- Single table `rooms` (see `migrations/001_init.sql` / `db/db.go`). `yjs_state`
  BYTEA holds the encoded update log.
- Schema is applied **two** ways: the backend auto-migrates on startup
  (`db.Migrate`), and `make migrate` runs `migrations/*.sql` via `psql`
  independently. Keep both in sync, and keep every migration idempotent
  (`CREATE TABLE IF NOT EXISTS …`).

## Environment & deploy
- Config via env (`.env`, copy from `.env.example`): `PORT`, `DB_URL`,
  `REDIS_URL`, `ENV` (`development` enables Vite-origin CORS + dev WS origin),
  `ALLOWED_ORIGINS` (comma-separated extra WS origins). Vite browser vars must be
  `VITE_`-prefixed.
- `Dockerfile` is 3-stage: build SPA (node) → build static Go binary
  (`CGO_ENABLED=0`, lib/pq is pure Go) → minimal alpine runtime mirroring the
  `/app/frontend/dist` layout the binary expects. Runs as non-root.

## Gotchas / current state vs. the design doc
- **Redis is connected but unused.** `main.go` pings Redis (and degrades
  gracefully if absent), but the hub is built with `NewHub(pool)` only — the
  pub/sub fan-out and presence-TTL described in `../CLAUDE.md` are **not yet
  wired in**. The relay is effectively single-instance today. If you add
  multi-instance support, that's where it goes.
- Rooms are **never** created over WebSocket — `/ws/:roomId` validates the id is
  a UUID and that the room already exists in the DB, else closes with a 1008.
- The app is intentionally anonymous/open — no auth. Don't add it without being
  asked.
