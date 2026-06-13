package http

import "net/http"

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
