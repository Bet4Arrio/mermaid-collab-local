# syntax=docker/dockerfile:1

# ─────────────────────────────────────────────────────────────────────────────
# Stage 1 — build the React/Vite SPA.
# Vite is configured to emit into ../backend/frontend/dist, so we lay the repo
# out the same way it lives on disk and let the build land in /src/backend.
# ─────────────────────────────────────────────────────────────────────────────
FROM node:20-alpine AS frontend

WORKDIR /src/frontend

# Install deps first for layer caching.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

# Build (tsc -b && vite build). outDir resolves to /src/backend/frontend/dist.
COPY frontend/ ./
RUN npm run build

# ─────────────────────────────────────────────────────────────────────────────
# Stage 2 — compile the Go single binary.
# ─────────────────────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS backend

WORKDIR /src/backend

# Module deps first for layer caching.
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Source + the SPA build output (so any embedded checks see it; runtime serves
# it from disk regardless).
COPY backend/ ./
COPY --from=frontend /src/backend/frontend/dist ./frontend/dist

# Static binary, no CGO (lib/pq is pure Go).
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/mermaid-collab .

# ─────────────────────────────────────────────────────────────────────────────
# Stage 3 — minimal runtime image.
# The binary expects ./frontend/dist relative to its working dir (see Makefile
# `run` target), so we mirror that layout under /app.
# ─────────────────────────────────────────────────────────────────────────────
FROM alpine:3.20

# CA certs for any outbound TLS; non-root user.
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 app

WORKDIR /app

COPY --from=backend /out/mermaid-collab ./mermaid-collab
COPY --from=backend /src/backend/frontend/dist ./frontend/dist

USER app

ENV PORT=3000 \
    ENV=production
EXPOSE 3000

ENTRYPOINT ["./mermaid-collab"]
