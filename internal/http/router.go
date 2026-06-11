package http

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

// Pinger is the minimal capability readyz needs to check downstream health.
// *pgxpool.Pool satisfies it, so this package stays decoupled from pgx.
type Pinger interface {
	Ping(ctx context.Context) error
}

// NewRouter wires every route to the given service and returns the handler tree.
func NewRouter(svc *shortener.Service, pinger Pinger, logger *slog.Logger) http.Handler {
	h := &handler{svc: svc, pinger: pinger, logger: logger}

	router := chi.NewRouter()
	router.Get("/healthz", healthz)
	router.Get("/readyz", h.readyz)
	router.Post("/api/links", h.createLink)
	router.Get("/{code}", h.redirect)

	return router
}

// healthz is the liveness probe — dependency-free, so a downstream blip never triggers a restart.
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// readyz reports 200 only if the database answers, so a load balancer can stop
// routing to an instance whose DB is unreachable without killing it.
func (h *handler) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.pinger.Ping(ctx); err != nil {
		h.logger.Error("readiness check failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
