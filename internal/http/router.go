package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

// NewRouter wires every route to the given service and returns the handler tree.
func NewRouter(svc *shortener.Service, logger *slog.Logger) http.Handler {
	h := &handler{svc: svc, logger: logger}

	router := chi.NewRouter()
	router.Get("/healthz", healthz)
	router.Get("/readyz", readyz)
	router.Post("/api/links", h.createLink)
	router.Get("/{code}", h.redirect)

	return router
}

// healthz is the liveness probe — dependency-free so a downstreamxw never triggers a restart.
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// readyz is the readiness probe. It gains a DB check in 1.6; for now it's always ready.
func readyz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
