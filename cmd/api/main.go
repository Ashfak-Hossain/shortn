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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	sqids "github.com/sqids/sqids-go"

	"github.com/Ashfak-Hossain/shortn/internal/cache"
	"github.com/Ashfak-Hossain/shortn/internal/config"
	"github.com/Ashfak-Hossain/shortn/internal/events"
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

// requestTimeout is the hard ceiling for any single HTTP request's downstream
// work. A redirect should resolve in single-digit milliseconds, so 2s is a
// failure ceiling — not a target — that stops one frozen dependency from
// parking goroutines and draining the pgx pool.
const requestTimeout = 2 * time.Second

func main() {
	// Failing fast here prevents the application from booting in an invalid state.
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	if cfg.WorkerID == "" {
		slog.Error("WORKER_ID is required")
		os.Exit(1)
	}
	wid, err := strconv.ParseUint(cfg.WorkerID, 10, 16)
	if err != nil || wid > 1023 {
		slog.Error("WORKER_ID must be an integer in [0, 1023]", "value", cfg.WorkerID)
		os.Exit(1)
	}
	sq, err := sqids.New(sqids.Options{Alphabet: cfg.SqidsAlphabet})
	if err != nil {
		slog.Error("failed to initialise sqids encoder", "err", err)
		os.Exit(1)
	}
	gen, err := idgen.NewSnowflakeGenerator(uint16(wid), sq)

	if err != nil {
		slog.Error("failed to create ID generator", "err", err)
		os.Exit(1)
	}

	// JSON format ensures machine-readable output in prod.
	// We set this as the default logger so standard library logs capture the same format.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	// pgxpool.New establishes the configuration but connects lazily.
	// We mandate an immediate Ping to ensure the database is reachable on startup,
	// preventing the application from accepting traffic when the DB is down.
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to create db pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	if err := pool.Ping(pingCtx); err != nil {
		logger.Error("database not reachable", "err", err)
		os.Exit(1)
	}

	// redis.ParseURL turns the DSN into options (pool size, db index, etc.); go-redis connects lazily on first use.
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("invalid REDIS_URL", "err", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(opts)
	defer func() {
		if err := rdb.Close(); err != nil {
			logger.Warn("failed to close redis client", "err", err)
		}
	}()

	// Unlike Postgres, a Redis outage is NOT fatal — the cache is an optimization,
	// not a dependency. We ping only to surface a warning; we keep booting either way.
	// This is the "fail open" principle enforced at startup.
	redisPingCtx, cancelRedisPing := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRedisPing()
	if err := rdb.Ping(redisPingCtx).Err(); err != nil {
		logger.Warn("redis not reachable at startup; serving uncached from postgres", "err", err)
	}

	st := store.New(pool)
	cachingStore := cache.NewCachingStore(st, cache.New(rdb), cacheTTL, logger)
	svc := shortener.NewService(cachingStore, gen) // service gets the cache-wrapped store, not the raw one

	// Like the pgx pool and redis client, the franz-go client connects lazily, so this
	// only errors on bad config — a broker that is *down* surfaces later at Publish time
	// (logged, non-fatal), never here.
	pub, err := events.NewKafkaPublisher(strings.Split(cfg.KafkaBrokers, ","), cfg.KafkaTopic)
	if err != nil {
		logger.Error("failed to create kafka publisher", "err", err)
		os.Exit(1)
	}
	defer pub.Close()

	router := httpapi.NewRouter(svc, pool, logger, cfg.InstanceID, requestTimeout, pub)

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

	// A separate goroutine leaves the main thread free to block on OS signals below.
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()
	logger.Info("server started", "addr", addr, "env", cfg.Env)

	// Block until SIGINT (Ctrl+C) or SIGTERM (Docker/K8s stop).
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
