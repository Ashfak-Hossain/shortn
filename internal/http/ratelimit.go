package http

import (
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ashfak-Hossain/shortn/internal/ratelimit"
)

// RateLimitMiddleware rejects a client that has drained its token bucket with a
// 429 and a Retry-After header. If Redis is unreachable it fails open (allows
// the request): the limiter is abuse protection, not a correctness dependency,
// so its outage must not take the service down.
func RateLimitMiddleware(limiter *ratelimit.Limiter, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)

			allowed, retryAfter, err := limiter.Allow(r.Context(), ip)
			if err != nil {
				logger.Warn("rate limiter unavailable; allowing request", "err", err, "ip", ip)
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// clientIP returns the originating client's address, used as the rate-limit key.
//
// In every deployment exactly one trusted proxy sits directly in front of the
// API — nginx in compose, Traefik on k3s — and BOTH append the address they saw
// connecting to the end of X-Forwarded-For. That right-most entry is therefore
// the real edge client, and it is spoof-resistant: a client that forges an
// X-Forwarded-For only prepends entries; the proxy still appends the true
// address after them, so the last one wins. We deliberately do NOT trust
// X-Real-IP — Traefik neither sets nor strips it, so on that path a client could
// forge it to evade the limiter or poison another client's bucket.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// retryAfterSeconds rounds a wait up to whole seconds for the Retry-After
// header (second-granularity), never returning less than 1.
func retryAfterSeconds(d time.Duration) int {
	if s := int(math.Ceil(d.Seconds())); s > 1 {
		return s
	}
	return 1
}
