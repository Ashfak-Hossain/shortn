package http

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// ServedByMiddleware writes the X-Served-By response header on every request,
// identifying which instance handled it.
func ServedByMiddleware(instanceID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Served-By", instanceID)
			next.ServeHTTP(w, r)
		})
	}
}

// TimeoutMiddleware bounds every request to d by swapping the request's context
// for one carrying a deadline. The whole chain below (handler → service →
// store/cache → pgx/redis) already threads this context, so a frozen dependency
// surfaces as a context.DeadlineExceeded error instead of a hung goroutine.
func TimeoutMiddleware(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			// release the timer immediately when the handler finishes before the deadline.
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RouteTagMiddleware stamps chi's matched route template (such as "/{code}") onto the
// active server span and the otelhttp metric labeler. otelhttp can't know the route
// on its own — and the raw value must never be the label: putting the short code in
// http.route would mint one Prometheus time-series per link and OOM it. The template
// is bounded; the code is not.
func RouteTagMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)

		// chi only finishes building the pattern as it descends to the matched
		// route, so read it AFTER the handler runs. otelhttp records its metric and
		// ends the span only after this returns, so the label still lands in time.
		pattern := chi.RouteContext(r.Context()).RoutePattern()
		if pattern == "" {
			return // unmatched (404) — no bounded template to attach
		}
		if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
			labeler.Add(semconv.HTTPRoute(pattern))
		}
		trace.SpanFromContext(r.Context()).SetName(pattern)
	})
}

// traceableRoute keeps health probes and the Prometheus scrape endpoint out of
// traces and request metrics — they fire constantly and say nothing about
// user-facing latency, so they'd only drown the golden signals in noise.
func traceableRoute(r *http.Request) bool {
	switch r.URL.Path {
	case "/healthz", "/readyz", "/metrics":
		return false
	}
	return true
}
