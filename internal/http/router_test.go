package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthz checks the liveness probe returns 200 and stays dependency-free.
func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	healthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
}
