// Package main is the primary HTTP service entrypoint for the shortn application.
// It wires configuration, structured logging, the database pool, the Redis cache,
// domain services, and the HTTP router, then runs the server through to a graceful
// shutdown.
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
	"github.com/Ashfak-Hossain/shortn/internal/idempotency"
	"github.com/Ashfak-Hossain/shortn/internal/idgen"
	"github.com/Ashfak-Hossain/shortn/internal/observability"
	"github.com/Ashfak-Hossain/shortn/internal/ratelimit"
	"github.com/Ashfak-Hossain/shortn/internal/resilience"
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

// idempotencyTTL is how long an Idempotency-Key is remembered — long enough to
// cover client retries, short enough to self-clean.
const idempotencyTTL = 24 * time.Hour

func main() {
	// ============================================================
	// CONFIGURATION & ID GENERATOR
	// ============================================================

	// Fail fast: a bad config or worker ID must never boot into a running state.
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

	// ============================================================
	// LOGGING
	// ============================================================

	// JSON output is machine-readable for prod log shipping. Registering it as the
	// default logger means stdlib log calls share the same format.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	// ============================================================
	// OBSERVABILITY (OpenTelemetry)
	// ============================================================

	// Set up OTel early so every component below can emit signals. The metric
	// reader is local (no network); the trace exporter connects lazily, so an
	// unreachable Tempo never blocks startup.
	providers, err := observability.Setup(context.Background(), observability.Config{
		ServiceName:  "shortn-api",
		InstanceID:   cfg.InstanceID,
		OTLPEndpoint: cfg.OTELEndpoint,
	})
	if err != nil {
		logger.Error("failed to init observability", "err", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := providers.Shutdown(shutdownCtx); err != nil {
			logger.Warn("observability shutdown failed", "err", err)
		}
	}()

	// ============================================================
	// DATABASE (Postgres)
	// ============================================================

	// pgxpool connects lazily, so we Ping immediately: Postgres is a hard
	// dependency, and we refuse to accept traffic if it is unreachable at startup.
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

	// ============================================================
	// CACHE (Redis)
	// ============================================================

	// ParseURL turns the DSN into options (pool size, db index, …); go-redis
	// connects lazily on first use.
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("invalid REDIS_URL", "err", err)
		os.Exit(1)
	}
	// Short, explicit timeouts (go-redis defaults to a 3s read) make a hung Redis
	// fail fast so callers fail open — serve from Postgres, skip the rate limit —
	// well within the request budget. Local Redis answers in well under a
	// millisecond, so this is huge headroom for normal operation.
	opts.DialTimeout = 300 * time.Millisecond
	opts.ReadTimeout = 200 * time.Millisecond
	opts.WriteTimeout = 200 * time.Millisecond
	opts.PoolTimeout = 300 * time.Millisecond
	rdb := redis.NewClient(opts)
	defer func() {
		if err := rdb.Close(); err != nil {
			logger.Warn("failed to close redis client", "err", err)
		}
	}()

	// A Redis outage is NOT fatal — the cache is an optimization, not a dependency.
	// We ping only to surface a warning and keep booting either way ("fail open").
	redisPingCtx, cancelRedisPing := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRedisPing()
	if err := rdb.Ping(redisPingCtx).Err(); err != nil {
		logger.Warn("redis not reachable at startup; serving uncached from postgres", "err", err)
	}

	// ============================================================
	// DOMAIN SERVICE WIRING
	// ============================================================

	// Compose the store from the inside out: raw pgx store → circuit breaker →
	// Redis cache. The service receives the cache-wrapped store, never the raw one.
	st := store.New(pool)
	resilient := resilience.NewResilientStore(st, logger)
	cachingStore := cache.NewCachingStore(resilient, cache.New(rdb), cacheTTL, logger)
	svc := shortener.NewService(cachingStore, gen)

	// ============================================================
	// EVENT PUBLISHER (Kafka / Redpanda)
	// ============================================================

	// Like the pgx pool and redis client, the franz-go client connects lazily, so
	// this only errors on bad config — a broker that is *down* surfaces later at
	// Publish time (logged, non-fatal), never here.
	pub, err := events.NewKafkaPublisher(strings.Split(cfg.KafkaBrokers, ","), cfg.KafkaTopic)
	if err != nil {
		logger.Error("failed to create kafka publisher", "err", err)
		os.Exit(1)
	}
	defer pub.Close()

	// ============================================================
	// RATE LIMITER & IDEMPOTENCY
	// ============================================================

	burst, err := strconv.Atoi(cfg.RateLimitBurst)
	if err != nil || burst < 1 {
		logger.Error("RATE_LIMIT_BURST must be a positive integer", "value", cfg.RateLimitBurst)
		os.Exit(1)
	}
	rps, err := strconv.Atoi(cfg.RateLimitRPS)
	if err != nil || rps < 1 {
		logger.Error("RATE_LIMIT_RPS must be a positive integer", "value", cfg.RateLimitRPS)
		os.Exit(1)
	}
	limiter := ratelimit.New(rdb, burst, rps)

	idem := idempotency.New(rdb, idempotencyTTL)

	// ============================================================
	// HTTP ROUTER & SERVER
	// ============================================================

	router := httpapi.NewRouter(httpapi.RouterDeps{
		Service:        svc,
		Pinger:         pool,
		Logger:         logger,
		InstanceID:     cfg.InstanceID,
		RequestTimeout: requestTimeout,
		Limiter:        limiter,
		Idempotency:    idem,
		Publisher:      pub,
		MetricsHandler: providers.MetricsHandler,
	})

	// Strict server timeouts mitigate slowloris attacks and stop stale or malicious
	// connections from exhausting resources.
	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// ============================================================
	// STARTUP & GRACEFUL SHUTDOWN
	// ============================================================

	// Serve in a separate goroutine so the main thread is free to block on signals.
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

	// Give in-flight requests up to 10s to finish before forcing termination.
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
