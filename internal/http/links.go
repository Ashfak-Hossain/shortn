// Package http implements the HTTP delivery layer for the API.
// It is strictly responsible for decoding network requests, translating
// domain-level errors into appropriate HTTP status codes, and formatting
// JSON responses for the client.
package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
	"github.com/go-chi/chi/v5"
)

// handler serves as the central dependency container for all API routes.
// It holds the domain service and logger so they can be shared safely
// across concurrent HTTP requests.
type handler struct {
	svc    *shortener.Service
	pinger Pinger
	logger *slog.Logger
}

// createRequest defines the expected JSON payload for link creation.
// We intentionally define a dedicated Data Transfer Object (DTO) here rather
// than using the domain's Link struct to prevent internal database fields
// (like ID or ClickCount) from accidentally leaking into the public API contract.
type createRequest struct {
	URL string `json:"url"`
}

// createResponse defines the JSON payload returned upon successful link creation.
type createResponse struct {
	Code     string `json:"code"`
	ShortURL string `json:"short_url"`
	LongURL  string `json:"long_url"`
}

// createLink handles the POST /api/links endpoint.
// It validates the incoming JSON, delegates the business logic to the domain service,
// and constructs the HTTP response.
func (h *handler) createLink(w http.ResponseWriter, r *http.Request) {
	var req createRequest

	// We strictly enforce valid JSON parsing and fail fast if the client
	// sends a malformed payload.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}

	// Basic structural validation at the HTTP boundary to save
	// unnecessary processing
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	link, err := h.svc.Create(r.Context(), req.URL)
	if err != nil {
		// We translate safe, expected domain errors into 4xx client errors
		// so the user knows how to correct their request.
		if errors.Is(err, shortener.ErrInvalidURL) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		// For unhandled internal failures, we log the exact error for debugging,
		// but we purposefully return a generic 500 response to the client to
		// prevent leaking sensitive system or database details.
		h.logger.Error("create link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create link")
		return
	}

	writeJSON(w, http.StatusCreated, createResponse{
		Code:     link.Code,
		ShortURL: shortURL(r, link.Code),
		LongURL:  link.LongURL,
	})
}

// redirect handles the GET /{code} endpoint.
// It looks up the destination URL and issues the appropriate HTTP redirect.
func (h *handler) redirect(w http.ResponseWriter, r *http.Request) {
	// We extract the URL parameter securely using the router's context.
	code := chi.URLParam(r, "code")

	link, err := h.svc.Resolve(r.Context(), code)
	if err != nil {
		// We translate the domain's 'Not Found' sentinel into a standard 404.
		if errors.Is(err, shortener.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no link for that code")
			return
		}
		h.logger.Error("resolve link failed", "err", err, "code", code)
		writeError(w, http.StatusInternalServerError, "could not resolve link")
		return
	}

	// We intentionally use a 302 Found (Temporary Redirect) instead of a 301
	// (Permanent Redirect). A 301 is aggressively cached by web browsers, which
	// would cause future visits to bypass our server entirely, silently breaking
	// our ability to track click analytics.
	http.Redirect(w, r, link.LongURL, http.StatusFound)
}

// shortURL dynamically constructs the absolute, shortened URL string.
func shortURL(r *http.Request, code string) string {
	scheme := "http"

	// If the incoming request was encrypted via TLS, we upgrade the scheme to ensure
	// the generated link matches the security context of the user's session.
	if r.TLS != nil {
		scheme = "https"
	}

	// We dynamically infer the domain using the request's Host header rather than
	// relying on a hardcoded configuration variable. This ensures the application
	// remains entirely portable across local dev, staging, and prod.
	return fmt.Sprintf("%s://%s/%s", scheme, r.Host, code)
}
