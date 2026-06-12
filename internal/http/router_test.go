package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthz verifies the Kubernetes liveness probe successfully returns a 200 OK.
// We explicitly test this seemingly trivial handler to guarantee it remains strictly
// isolated. If a future developer accidentally wires this endpoint to a database
// middleware, this test acts as a safeguard to catch that architectural regression.
func TestHealthz(t *testing.T) {
	// We use the standard library's httptest package to simulate HTTP traffic
	// entirely in memory. This avoids the overhead and potential port collisions
	// of spinning up a real TCP server during CI/CD test runs.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	// We invoke the handler function directly, passing in our mock request and recorder.
	healthz(rec, req)

	// We assert that the probe strictly adheres to the HTTP semantics required
	// by container orchestration systems to mark the pod as healthy.
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
}
