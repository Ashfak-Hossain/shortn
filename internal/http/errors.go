// Package http contains the error response struct for all error responses in the API.
package http

import (
	"encoding/json"
	"net/http"
)

// This is the json body for all error responses.
type errorResponse struct {
	Error string `json:"error"`
}

// writeError writes a JSON error response with the given status and message.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response with the given status and message.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}
