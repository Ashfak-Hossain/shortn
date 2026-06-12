// Package http provides standard HTTP delivery mechanisms, including
// unified JSON serialization and error handling for the API layer.
package http

import (
	"encoding/json"
	"net/http"
)

// errorResponse defines the standard JSON payload structure for all API errors.
// Standardizing this ensures frontend clients and downstream consumers can
// predictably parse and handle failure states.
type errorResponse struct {
	Error string `json:"error"`
}

// writeJSON serializes an arbitrary data payload into JSON and writes it
// to the HTTP response with the specified status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	// We must explicitly set the Content-Type header before calling WriteHeader.
	// If we do not, the standard library's HTTP server will attempt to sniff
	// the bytes and may incorrectly default to "text/plain".
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	// We intentionally suppress the error from json.Encode using the blank identifier.
	// Because WriteHeader has already been called, the HTTP status is locked in.
	// If encoding fails now, we cannot retroactively change the response to a 500 error,
	// so the best course of action is to let the connection close naturally.
	_ = json.NewEncoder(w).Encode(v)
}

// writeError constructs a standardized JSON error payload and writes it
// to the client.
func writeError(w http.ResponseWriter, status int, message string) {
	// We delegate to writeJSON to guarantee that both successful responses
	// and error responses share the exact same HTTP header configuration.
	writeJSON(w, status, errorResponse{Error: message})
}
