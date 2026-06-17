// Package http implements the HTTP delivery layer for the application.
// It provides the routing, handler logic, and operational health probes required
// to expose the core domain service over the web.
package http

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Ashfak-Hossain/shortn/internal/events"
	"github.com/Ashfak-Hossain/shortn/internal/ratelimit"
	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

// Pinger is implemented by any value that can verify connectivity to a downstream
// dependency. Ping must return a non-nil error if the dependency is unreachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Publisher publishes a click event. Implemented by [*events.KafkaPublisher].
type Publisher interface {
	Publish(ctx context.Context, e events.LinkClicked) error
}

// IdempotencyStore remembers which short code an Idempotency-Key produced, so a
// retried create returns the same link. Implemented by [*idempotency.Store].
type IdempotencyStore interface {
	Get(ctx context.Context, key string) (code string, found bool, err error)
	Set(ctx context.Context, key, code string) (won bool, err error)
}

// RouterDeps bundles everything NewRouter needs. A struct keeps the call site
// readable as named fields instead of a long positional argument list.
type RouterDeps struct {
	Service        *shortener.Service
	Pinger         Pinger
	Logger         *slog.Logger
	InstanceID     string // attached to every response as the X-Served-By header
	RequestTimeout time.Duration
	Limiter        *ratelimit.Limiter
	Idempotency    IdempotencyStore
	Publisher      Publisher
	MetricsHandler http.Handler // promhttp handler; mounted at GET /metrics
}

// NewRouter returns a fully configured [http.Handler] with all application routes registered.
func NewRouter(d RouterDeps) http.Handler {
	// Bind the injected deps to our handler struct so they are
	// safely accessible to the individual route methods.
	h := &handler{svc: d.Service, pinger: d.Pinger, logger: d.Logger, publisher: d.Publisher, idem: d.Idempotency}

	router := chi.NewRouter()

	router.Use(ServedByMiddleware(d.InstanceID))
	router.Use(TimeoutMiddleware(d.RequestTimeout))

	// Op endpoints
	router.Get("/healthz", healthz)
	router.Get("/readyz", h.readyz)

	// prometheus scrapes this.
	router.Method(http.MethodGet, "/metrics", d.MetricsHandler)

	// client-facing sits behind the rate limiter
	router.Group(func(r chi.Router) {
		r.Use(RateLimitMiddleware(d.Limiter, d.Logger))

		// API endpoints
		r.Post("/api/links", h.createLink)
		r.Get("/{code}", h.redirect)

		// Analytics
		r.Get("/api/links/{code}/stats", h.stats)
	})

	return router
}

// healthz implements a standard Kubernetes liveness probe.
// It purposefully checks zero downstream deps. This guarantees that a
// temporary network blip to the db does not trigger orchestration systems
// to aggressively kill and restart the application container.
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// readyz implements a standard Kubernetes readiness probe.
// It actively verifies connectivity to critical downstream deps.
// If the db is unreachable, it returns a 503, instructing load balancers
// to temporarily stop routing traffic to this instance without terminating the process.
func (h *handler) readyz(w http.ResponseWriter, r *http.Request) {
	// We enforce a strict, short timeout to prevent the readiness check from hanging
	// indefinitely and exhausting server resources if the network or database is frozen.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.pinger.Ping(ctx); err != nil {
		h.logger.Error("readiness check failed", "err", err)

		// We explicitly return a 503 Service Unavailable so the load balancer
		// accurately interprets this as a failed readiness state.
		writeError(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
