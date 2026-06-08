package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Ashfak-Hossain/shortn/internal/config"
)

// healthz returns "ok" if the server is running.
func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK) // set status code to 200
	_, _ = w.Write([]byte("ok"))
}

// readyz returns "ok" if the server is ready to accept requests.
func readyz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK) // set status code to 200
	_, _ = w.Write([]byte("ok"))
}

func main() {
	// Load configuration from env
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	// Set up structured logging with slog
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	// Set up HTTP server with chi router
	r := chi.NewRouter()
	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz)

	// Create an http.Server with timeouts
	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start the server and log any errors
	logger.Info("Server starting", "addr", addr, "env", cfg.Env)
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("Server failed to start", "err", err)
		os.Exit(1)
	}
}

// parseLevel converts a string log level to slog.Level.
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
