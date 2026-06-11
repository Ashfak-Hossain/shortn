// Command api is the shortn HTTP service entrypoint. It wires configuration,
// structured logging, the database pool, the domain service, the HTTP router,
// and an HTTP server with graceful shutdown on SIGINT/SIGTERM.
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

	"github.com/Ashfak-Hossain/shortn/internal/config"
	httpapi "github.com/Ashfak-Hossain/shortn/internal/http"
	"github.com/Ashfak-Hossain/shortn/internal/idgen"
	"github.com/Ashfak-Hossain/shortn/internal/shortener"
	"github.com/Ashfak-Hossain/shortn/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	// pgxpool.New only parses the DSN and connects lazily, so we Ping to fail
	// fast on a bad URL or an unreachable database.
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

	// Compose the layers: store + generator -> domain service -> HTTP router.
	st := store.New(pool)
	gen := idgen.NewRandomBase62(7)
	svc := shortener.NewService(st, gen)
	router := httpapi.NewRouter(svc, pool, logger)

	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()
	logger.Info("server started", "addr", addr, "env", cfg.Env)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	logger.Info("shutdown signal received, draining connections")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	logger.Info("server stopped cleanly")
}

// parseLevel maps a LOG_LEVEL string to an slog.Level, defaulting to Info.
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
