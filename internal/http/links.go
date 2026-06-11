// Package http contains the HTTP handlers for the API.
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

// handler is the root HTTP handler. Holds the dependencies that route handler share.
type handler struct {
	svc    *shortener.Service
	pinger Pinger
	logger *slog.Logger
}

// createRequest is the JSON body for the create endpoint.
type createRequest struct {
	URL string `json:"url"`
}

// createResponse is the JSON body for the create endpoint response.
type createResponse struct {
	Code     string `json:"code"`
	ShortURL string `json:"short_url"`
	LongURL  string `json:"long_url"`
}

// createLink handles POST /: it validates the request, calls the domain to create a link, and writes the response.
func (h *handler) createLink(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}

	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	link, err := h.svc.Create(r.Context(), req.URL)
	if err != nil {
		if errors.Is(err, shortener.ErrInvalidURL) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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

// redirect handles GET /{code}: it looks up the code and redirects to the long URL, or returns 404 if not found.
func (h *handler) redirect(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")

	link, err := h.svc.Resolve(r.Context(), code)
	if err != nil {
		if errors.Is(err, shortener.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no link for that code")
			return
		}
		h.logger.Error("resolve link failed", "err", err, "code", code)
		writeError(w, http.StatusInternalServerError, "could not resolve link")
		return
	}

	// 302, not 301: temporary and uncached, so every click reaches for click analytics
	http.Redirect(w, r, link.LongURL, http.StatusFound)
}

// shortURL constructs the short URL for a code, using the request's Host and scheme (http or https).
func shortURL(r *http.Request, code string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/%s", scheme, r.Host, code)
}
