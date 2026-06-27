package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAdminAuthMiddleware locks in the gate's contract: only a request carrying
// the exact configured key reaches the protected handler. The "server key unset"
// case is the important one — it proves the gate FAILS CLOSED, so forgetting to
// configure ADMIN_KEY locks the door rather than throwing it open.
func TestAdminAuthMiddleware(t *testing.T) {
	const key = "s3cr3t-operator-key"

	// next records whether the request made it past the gate.
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name      string
		serverKey string
		header    string
		setHeader bool
		wantCode  int
		wantPass  bool
	}{
		{"valid key passes", key, key, true, http.StatusOK, true},
		{"wrong key rejected", key, "not-the-key", true, http.StatusUnauthorized, false},
		{"missing header rejected", key, "", false, http.StatusUnauthorized, false},
		{"empty header rejected", key, "", true, http.StatusUnauthorized, false},
		{"server key unset fails closed", "", key, true, http.StatusUnauthorized, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false

			req := httptest.NewRequest(http.MethodGet, "/api/links", nil)
			if tc.setHeader {
				req.Header.Set("X-Admin-Key", tc.header)
			}
			rec := httptest.NewRecorder()

			AdminAuthMiddleware(tc.serverKey)(next).ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if reached != tc.wantPass {
				t.Errorf("reached handler = %v, want %v", reached, tc.wantPass)
			}
		})
	}
}
