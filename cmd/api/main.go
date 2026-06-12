// Package main is the primary HTTP service entrypoint for the shortn application.
// It is responsible for wiring application configuration, structured logging,
// the database connection pool, domain services, the HTTP router, and managing
// the HTTP server lifecycle, including graceful shutdowns.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Ashfak-Hossain/shortn/internal/cache"
	"github.com/Ashfak-Hossain/shortn/internal/config"
	httpapi "github.com/Ashfak-Hossain/shortn/internal/http"
	"github.com/Ashfak-Hossain/shortn/internal/idgen"
	"github.com/Ashfak-Hossain/shortn/internal/shortener"
	"github.com/Ashfak-Hossain/shortn/internal/store"
)

// cacheTTL is how long a resolved link stays in Redis before it self-expires.
// One hour is a deliberate tradeoff: long enough that hot links almost always
// hit the cache, short enough that an edited/deleted link self-heals quickly
// even if an invalidation were ever missed.
const cacheTTL = time.Hour

func main() {
	// Load application settings from the environment.
	// Failing fast here prevents the application from booting in an invalid state.
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	// Initialize a JSON logger for machine-readable output in prod.
	// We set this as the default logger so standard library logs capture the same format.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	// pgxpool. New establishes the configuration but connects lazily.
	// We mandate an immediate Ping to ensure the database is reachable on startup,
	// preventing the application from accepting traffic when the DB is down.
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to create db pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close() // scheduled to run when main() returns

	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	if err := pool.Ping(pingCtx); err != nil {
		logger.Error("database not reachable", "err", err)
		os.Exit(1)
	}

	// Build the Redis client. redis.ParseURL turns the DSN into options
	// (pool size, db index, etc.); go-redis connects lazily on first use.
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("invalid REDIS_URL", "err", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	// Unlike Postgres, a Redis outage is NOT fatal — the cache is an optimization,
	// not a dependency. We ping only to surface a warning; we keep booting either way.
	// This is the "fail open" principle enforced at startup.
	redisPingCtx, cancelRedisPing := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRedisPing()
	if err := rdb.Ping(redisPingCtx).Err(); err != nil {
		logger.Warn("redis not reachable at startup; serving uncached from postgres", "err", err)
	}

	// We instantiate the core domain logic, injecting the necessary data store
	// and utility dependencies to compose the application layers.
	st := store.New(pool)
	cachingStore := cache.NewCachingStore(st, cache.New(rdb), cacheTTL, logger)
	gen := idgen.NewRandomBase62(7)
	svc := shortener.NewService(cachingStore, gen) // service gets the cache-wrapped store, not the raw one

	router := httpapi.NewRouter(svc, pool, logger)

	// We enforce strict HTTP server timeouts to mitigate slowloris attacks
	// and prevent resource exhaustion from stale or malicious client connections.
	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Run the HTTP server in a separate goroutine so the main thread remains unblocked
	// to listen for OS interrupt signals.
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()
	logger.Info("server started", "addr", addr, "env", cfg.Env)

	// Graceful Shutdown Handling
	// Block the main thread until a SIGINT (Ctrl+C) or SIGTERM (Docker/K8s shutdown) is received.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	logger.Info("shutdown signal received, draining connections")

	// Allow in-flight requests a maximum of 10 seconds to complete before forcefully terminating.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	logger.Info("server stopped cleanly")
}

// parseLevel translates a string-based logging level into an slog.Level,
// defaulting to slog.LevelInfo if the input is unrecognized.
func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
