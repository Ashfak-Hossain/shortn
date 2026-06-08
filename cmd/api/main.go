// Command api is the shortn HTTP service entrypoint. It wires configuration,
// structured logging, the chi router, and an HTTP server with graceful
// shutdown on SIGINT/SIGTERM.
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

	"github.com/go-chi/chi/v5"

	"github.com/Ashfak-Hossain/shortn/internal/config"
)

// healthz is the liveness probe: it answers "is the process up?" and stays
// dependency-free, so a blip in a downstream can never trigger a pod restart.
func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// readyz is the readiness probe: "can this instance serve traffic right now?"
// It always returns 200 today; later phases check dependencies here so the
// load balancer can drain an unready instance without killing it.
func readyz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Bad config means the process can't run correctly: fail fast.
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	// JSON to stdout from day one so the Phase 6 log stack can index by field
	// instead of parsing free-form text.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	r := chi.NewRouter()
	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz)

	// Explicit server (not http.ListenAndServe) so we keep a handle for
	// Shutdown. The timeouts are deliberate hardcoded defaults — not config —
	// that bound how long a slow client can hold a connection open.
	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// ListenAndServe blocks, so run it off the main goroutine. A clean Shutdown
	// makes it return ErrServerClosed — the expected success path — so only a
	// different error is fatal.
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()
	logger.Info("server started", "addr", addr, "env", cfg.Env)

	// Buffered by one so the runtime can deliver the signal even before we park
	// on the receive. SIGINT is Ctrl-C; SIGTERM is what an orchestrator sends.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig // block here until a signal arrives; the drain below does the real work

	// Stop accepting connections and let in-flight requests finish, but no
	// longer than the grace period. This is the drain sequence Kubernetes
	// relies on for zero-downtime rolling deploys.
	logger.Info("shutdown signal received, draining connections")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	logger.Info("server stopped cleanly")
}

// parseLevel maps a LOG_LEVEL string to an slog.Level, defaulting to Info for
// empty or unrecognized values.
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
