#!/usr/bin/env bash
# Apply SQL migrations against DB_URL using psql.
#
# Usage:
#   ./migrate.sh                  # loads .env (if present), runs all migrations
#   DB_URL=postgres://... ./migrate.sh
#
# Migrations in ./migrations are applied in lexical order (000_, 001_, ...).
# Each file should be idempotent (CREATE TABLE IF NOT EXISTS, etc.).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MIGRATIONS_DIR="${SCRIPT_DIR}/migrations"

# Load .env if DB_URL isn't already in the environment.
if [[ -z "${DB_URL:-}" && -f "${SCRIPT_DIR}/.env" ]]; then
  # shellcheck disable=SC1091
  set -a
  source "${SCRIPT_DIR}/.env"
  set +a
fi

if [[ -z "${DB_URL:-}" ]]; then
  echo "error: DB_URL is not set (export it or add it to .env)" >&2
  exit 1
fi

if ! command -v psql >/dev/null 2>&1; then
  echo "error: psql not found on PATH" >&2
  exit 1
fi

shopt -s nullglob
files=("${MIGRATIONS_DIR}"/*.sql)
shopt -u nullglob

if [[ ${#files[@]} -eq 0 ]]; then
  echo "no .sql files in ${MIGRATIONS_DIR}" >&2
  exit 1
fi

for f in "${files[@]}"; do
  echo "→ applying $(basename "$f")"
  psql "${DB_URL}" -v ON_ERROR_STOP=1 -f "$f"
done

echo "✓ migrations applied"
