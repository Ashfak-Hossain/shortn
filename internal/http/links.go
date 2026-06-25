// Package http implements the HTTP delivery layer for the API.
// It is strictly responsible for decoding network requests, translating
// domain-level errors into appropriate HTTP status codes, and formatting
// JSON responses for the client.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Ashfak-Hossain/shortn/internal/events"
	"github.com/Ashfak-Hossain/shortn/internal/shortener"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Pagination bounds for GET /api/links. A cap stops a client asking for the whole
// table in one request.
const (
	defaultListLimit = 50
	maxListLimit     = 100
)

// handler serves as the central dependency container for all API routes.
// It holds the domain service and logger so they can be shared safely
// across concurrent HTTP requests.
type handler struct {
	svc       *shortener.Service
	pinger    Pinger
	logger    *slog.Logger
	publisher Publisher
	idem      IdempotencyStore
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

// statsResponse is the JSON shape for GET /api/links/{code}/stats.
type statsResponse struct {
	Code   string           `json:"code"`
	Total  int64            `json:"total"`
	Series []bucketResponse `json:"series"`
}

type bucketResponse struct {
	Bucket time.Time `json:"bucket"`
	Count  int64     `json:"count"`
}

// linkSummary is one row in the dashboard's link list. Clicks are intentionally
// omitted — links.click_count is unused; real per-link totals come from the
// stats endpoint, not this list.
type linkSummary struct {
	Code      string    `json:"code"`
	ShortURL  string    `json:"short_url"`
	LongURL   string    `json:"long_url"`
	CreatedAt time.Time `json:"created_at"`
}

// listResponse wraps the page in an object (not a bare array) so we can add
// paging metadata later without breaking the JSON contract.
type listResponse struct {
	Links []linkSummary `json:"links"`
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

	// fast path: a retry with a code which already recorded returns the original link
	key := r.Header.Get("Idempotency-Key")
	if key != "" {
		if code, found, err := h.idem.Get(r.Context(), key); err != nil {
			h.logger.Warn("Idempotency lookup failed; proceeding", "err", err) // fail open
		} else if found {
			h.respondWithCode(w, r, code)
			return
		}
	}

	link, err := h.svc.Create(r.Context(), req.URL)
	if err != nil {
		if errors.Is(err, shortener.ErrInvalidURL) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		if serviceUnavailable(err) {
			writeError(w, http.StatusServiceUnavailable, "service temporarily unavailable")
			return
		}

		h.logger.Error("create link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create link")
		return
	}

	// Record key -> code. if another req claimed this key first (won == false),
	// return that winners link for concurrent retries same result
	if key != "" {
		if won, err := h.idem.Set(r.Context(), key, link.Code); err != nil {
			h.logger.Warn("Idempotency save failed", "err", err) // fail open
		} else if !won {
			if code, found, _ := h.idem.Get(r.Context(), key); found {
				h.respondWithCode(w, r, code)
				return
			}
		}
	}

	writeCreatedLink(w, r, link)
}

// redirect handles the GET /{code} endpoint.
// It looks up the destination URL and issues the appropriate HTTP redirect.
func (h *handler) redirect(w http.ResponseWriter, r *http.Request) {
	// We extract the URL parameter securely using the router's context.
	code := chi.URLParam(r, "code")

	link, err := h.svc.Resolve(r.Context(), code)
	if err != nil {
		if errors.Is(err, shortener.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no link for that code")
			return
		}
		if serviceUnavailable(err) {
			writeError(w, http.StatusServiceUnavailable, "service temporarily unavailable")
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

	// We publish the click after the redirect is on the wire, in a goroutine with a
	// background context, so analytics never adds latency to the user's request —
	// even if Redpanda is down. A publish failure is logged and swallowed: the
	// redirect already succeeded, and click-tracking is best-effort.
	event := events.LinkClicked{
		EventID:   uuid.NewString(),
		Code:      code,
		Timestamp: time.Now().UTC(),
		Referrer:  r.Referer(),
		UserAgent: r.UserAgent(),
		Version:   1,
	}
	// Detach from the request's cancellation but KEEP its trace context, so the
	// publish span joins THIS redirect's trace even though it runs after the handler
	// returns. context.Background() would orphan the publish into a separate trace;
	// r.Context() would be cancelled the instant we return.
	pubCtx := context.WithoutCancel(r.Context())
	go func() {
		if err := h.publisher.Publish(pubCtx, event); err != nil {
			h.logger.Error("publish click event failed", "err", err, "code", code)
		}
	}()
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

// writeCreatedLink writes a link as a 201 Created response — the shared shape for
// a fresh create and for an idempotent replay of an earlier one.
func writeCreatedLink(w http.ResponseWriter, r *http.Request, link *shortener.Link) {
	writeJSON(w, http.StatusCreated, createResponse{
		Code:     link.Code,
		ShortURL: shortURL(r, link.Code),
		LongURL:  link.LongURL,
	})
}

// serviceUnavailable reports whether err is a transient "shed load" condition —
// a blown request deadline or an open dependency breaker — that should answer
// 503 rather than a generic 500.
func serviceUnavailable(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, shortener.ErrUnavailable)
}

// stats handles GET /api/links/{code}/stats.
func (h *handler) stats(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	s, err := h.svc.Stats(r.Context(), code)
	if err != nil {
		if serviceUnavailable(err) {
			writeError(w, http.StatusServiceUnavailable, "service temporarily unavailable")
			return
		}
		h.logger.Error("get stats failed", "err", err, "code", code)
		writeError(w, http.StatusInternalServerError, "could not get stats")
		return
	}

	resp := statsResponse{Code: s.Code, Total: s.Total, Series: []bucketResponse{}}
	for _, b := range s.Series {
		resp.Series = append(resp.Series, bucketResponse{Bucket: b.Bucket, Count: b.Count})
	}
	writeJSON(w, http.StatusOK, resp)
}

// respondWithCode resolves an already-created code and writes it as a create response
func (h *handler) respondWithCode(w http.ResponseWriter, r *http.Request, code string) {
	link, err := h.svc.Resolve(r.Context(), code)
	if err != nil {
		h.logger.Error("idempotent replay: resolve failed", "code", code, "err", err)
		writeError(w, http.StatusInternalServerError, "could not load link")
		return
	}
	writeCreatedLink(w, r, link)
}

// listLinks handles GET /api/links — the dashboard's manage view. Links come back
// newest-first, paged with ?limit and ?offset.
func (h *handler) listLinks(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", defaultListLimit)
	if limit < 1 || limit > maxListLimit {
		limit = defaultListLimit
	}
	offset := queryInt(r, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	links, err := h.svc.List(r.Context(), limit, offset)
	if err != nil {
		if serviceUnavailable(err) {
			writeError(w, http.StatusServiceUnavailable, "service temporarily unavailable")
			return
		}
		h.logger.Error("list links failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not list links")
		return
	}

	// make(...) not nil, so an empty page serializes as [] rather than null.
	resp := listResponse{Links: make([]linkSummary, 0, len(links))}
	for _, l := range links {
		resp.Links = append(resp.Links, linkSummary{
			Code:      l.Code,
			ShortURL:  shortURL(r, l.Code),
			LongURL:   l.LongURL,
			CreatedAt: l.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// deleteLink handles DELETE /api/links/{code}: 204 on success, 404 when the code
// doesn't exist.
func (h *handler) deleteLink(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")

	err := h.svc.Delete(r.Context(), code)
	if err != nil {
		if errors.Is(err, shortener.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no link for that code")
			return
		}
		if serviceUnavailable(err) {
			writeError(w, http.StatusServiceUnavailable, "service temporarily unavailable")
			return
		}
		h.logger.Error("delete link failed", "err", err, "code", code)
		writeError(w, http.StatusInternalServerError, "could not delete link")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// queryInt reads an integer query parameter, returning def when it's absent or
// not a valid integer. The caller enforces bounds.
func queryInt(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
