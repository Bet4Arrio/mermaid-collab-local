SHELL := /bin/bash
.PHONY: dev build run install docker-up docker-down migrate clean

# Install frontend dependencies (Go deps resolve on build).
install:
	cd frontend && npm install

# Run backend (:3000) and Vite dev server (:5173) concurrently.
# Vite proxies /api and /ws to the backend. Ctrl-C stops both.
# Requires postgres/redis running — see `make docker-up`.
dev:
	@echo "→ backend on :3000, frontend on :5173 (Ctrl-C stops both)"
	@trap 'kill 0' EXIT INT TERM; \
		( cd backend && go run . ) & \
		( cd frontend && npm run dev ) & \
		wait

# Build the SPA into backend/frontend/dist, then compile the single binary.
build:
	cd frontend && npm install && npm run build
	cd backend && go build -o mermaid-collab .
	@echo "→ built backend/mermaid-collab (serves API + SPA)"

# Run the compiled single binary (must run from backend/ so ./frontend/dist resolves).
run:
	cd backend && ./mermaid-collab

docker-up:
	docker compose up -d

docker-down:
	docker compose down

# Apply the SQL schema via migrate.sh (uses DB_URL from .env). The backend also
# auto-migrates on startup; this target is for running migrations independently.
migrate:
	./migrate.sh

clean:
	rm -rf backend/frontend/dist
	rm -f backend/mermaid-collab
