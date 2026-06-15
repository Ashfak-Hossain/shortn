package http

import (
	"context"
	"net/http"
	"time"
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
			// release the timer; without this the context leaks until the
			// deadline fires even when the handler finishes early.
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
