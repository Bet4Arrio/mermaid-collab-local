package main

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/redis/go-redis/v9"

	"mermaid-collab/db"
	"mermaid-collab/handlers"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// originAllowed guards the WebSocket upgrade against Cross-Site WebSocket
// Hijacking. Browsers do not enforce same-origin on WS, so we check the Origin
// header ourselves: same-origin is always allowed; extra origins come from
// ALLOWED_ORIGINS; in development the Vite dev server is allowed too. Requests
// with no Origin (non-browser clients) are permitted — they cannot be a CSWSH.
func originAllowed(c *fiber.Ctx, allowlist map[string]bool, devMode bool) bool {
	origin := c.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Same-origin: the Origin host matches the request Host.
	if u.Host == string(c.Request().Host()) {
		return true
	}
	if devMode && (origin == "http://localhost:5173" || origin == "http://127.0.0.1:5173") {
		return true
	}
	return allowlist[origin]
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	port := env("PORT", "3000")
	dbURL := env("DB_URL", "postgres://postgres:postgres@localhost:5432/mermaid_collab?sslmode=disable")
	redisURL := env("REDIS_URL", "redis://localhost:6379")
	appEnv := env("ENV", "development")
	devMode := appEnv == "development"

	// Optional comma-separated extra WebSocket origins (e.g. a CDN host).
	allowlist := map[string]bool{}
	for _, o := range strings.Split(env("ALLOWED_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			allowlist[o] = true
		}
	}

	rootCtx := context.Background()

	// --- PostgreSQL ---------------------------------------------------------
	pool, err := db.Connect(rootCtx, dbURL)
	if err != nil {
		slog.Error("database connection failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := db.Migrate(rootCtx, pool); err != nil {
		slog.Error("migration failed", "err", err)
		os.Exit(1)
	}

	// --- Redis (pub/sub fan-out + presence TTL; optional in single-instance) -
	var rdb *redis.Client
	if opts, perr := redis.ParseURL(redisURL); perr != nil {
		slog.Warn("invalid REDIS_URL, continuing without redis", "err", perr)
	} else {
		rdb = redis.NewClient(opts)
		pingCtx, cancel := context.WithTimeout(rootCtx, 3*time.Second)
		if err := rdb.Ping(pingCtx).Err(); err != nil {
			slog.Warn("redis unreachable, continuing without it", "err", err)
			_ = rdb.Close()
			rdb = nil
		}
		cancel()
		if rdb != nil {
			defer rdb.Close()
			slog.Info("connected to redis")
		}
	}

	// --- Fiber app ----------------------------------------------------------
	app := fiber.New(fiber.Config{
		AppName:               "mermaid-collab",
		DisableStartupMessage: true,
		BodyLimit:             256 * 1024, // cap REST request bodies (DoS guard)
	})
	app.Use(recover.New())
	app.Use(logger.New())

	// CORS only matters in development (Vite dev server on :5173). In
	// production Go serves the SPA from the same origin.
	if appEnv == "development" {
		app.Use(cors.New(cors.Config{
			AllowOrigins: "http://localhost:5173",
			AllowHeaders: "Origin, Content-Type, Accept",
			AllowMethods: "GET, POST, DELETE, OPTIONS",
		}))
	}

	// --- WebSocket hub ------------------------------------------------------
	hub := handlers.NewHub(pool)

	app.Use("/ws", func(c *fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}
		if !originAllowed(c, allowlist, devMode) {
			slog.Warn("rejected websocket origin", "origin", c.Get("Origin"))
			return fiber.ErrForbidden
		}
		return c.Next()
	})
	app.Get("/ws/:roomId", websocket.New(hub.Handler()))

	// --- REST API -----------------------------------------------------------
	api := app.Group("/api")
	handlers.NewRoomsHandler(pool).Register(api)

	// --- Static SPA (must come after /api and /ws) --------------------------
	app.Static("/", "./frontend/dist")
	// SPA fallback: any unmatched GET returns index.html so client routing /
	// hard refreshes work.
	app.Get("*", func(c *fiber.Ctx) error {
		return c.SendFile("./frontend/dist/index.html")
	})

	// --- Start + graceful shutdown -----------------------------------------
	go func() {
		slog.Info("listening", "port", port, "env", appEnv)
		if err := app.Listen(":" + port); err != nil {
			slog.Error("server stopped", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down: persisting active rooms")

	hub.PersistAll()
	shutdownCtx, cancel := context.WithTimeout(rootCtx, 10*time.Second)
	defer cancel()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
	slog.Info("bye")
}
