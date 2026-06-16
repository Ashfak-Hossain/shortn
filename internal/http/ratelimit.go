package http

import (
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
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

// clientIP returns the originating client address. nginx sets X-Real-IP to the
// real connecting address ($remote_addr), which a client cannot spoof; we key
// the limiter on that. Falling back to RemoteAddr covers direct, non-proxied
// requests (e.g. local `make run`).
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
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
