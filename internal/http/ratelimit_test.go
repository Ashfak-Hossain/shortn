package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientIP proves the limiter keys on the right address behind a proxy. The
// load-bearing case is "forged left entry ignored": a client that prepends a
// fake X-Forwarded-For must not be able to choose its own rate-limit identity —
// the trusted proxy appends the real address last, so the right-most entry wins.
func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		xff        string // X-Forwarded-For header; "" means not set
		remoteAddr string
		want       string
	}{
		{"no proxy falls back to RemoteAddr", "", "203.0.113.7:54321", "203.0.113.7"},
		{"single proxy hop", "203.0.113.9", "10.0.0.1:5000", "203.0.113.9"},
		{"forged left entry ignored, proxy-appended right wins", "9.9.9.9, 203.0.113.9", "10.0.0.1:5000", "203.0.113.9"},
		{"surrounding spaces trimmed", "203.0.113.9 ", "10.0.0.1:5000", "203.0.113.9"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientIP(req); got != tc.want {
				t.Errorf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}
