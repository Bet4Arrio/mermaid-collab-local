package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/lib/pq"
)

// schema is applied idempotently on startup and via `make migrate`.
const schema = `
CREATE TABLE IF NOT EXISTS rooms (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title      TEXT NOT NULL,
    yjs_state  BYTEA,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
`

// Connect opens a PostgreSQL connection pool and verifies it with a ping.
func Connect(ctx context.Context, dbURL string) (*sql.DB, error) {
	pool, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	pool.SetMaxOpenConns(25)
	pool.SetMaxIdleConns(5)
	pool.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	slog.Info("connected to postgres")
	return pool, nil
}

// Migrate applies the schema. Safe to call repeatedly.
func Migrate(ctx context.Context, pool *sql.DB) error {
	if _, err := pool.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	slog.Info("migrations applied")
	return nil
}
