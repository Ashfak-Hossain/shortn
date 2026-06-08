package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthz tests the healthz handler to ensure it returns the expected status code.
func TestHealthz(t *testing.T) {
	cases := []struct {
		name     string
		wantCode int
	}{
		{name: "returns 200 OK", wantCode: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			rec := httptest.NewRecorder()

			healthz(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("got status %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}
