// Package shortener is the core domain logic of the URL shortener
package shortener

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// max retry attempts for code generation cz of collision returning an error.
const maxCreateRetries = 5

// Errors that the domain can return, for the HTTP layer to translate into status codes
var (
	ErrNotFound   = errors.New("link not found")
	ErrCodeExists = errors.New("code already exists")
	ErrInvalidURL = errors.New("invalid url")
)

// Link represents a URL mapping. It is the domain's core data structure.
type Link struct {
	ID         int64
	Code       string
	LongURL    string
	CreatedAt  time.Time
	ExpiresAt  *time.Time // nil = never expires (column exists; unused until later)
	ClickCount int64
}

// LinkStore defines the persistence interface for links.
type LinkStore interface {
	// Create persists a new link.
	Create(ctx context.Context, link *Link) error
	// returns the link for a code
	GetByCode(ctx context.Context, code string) (*Link, error)
}

// IDGenerator defines the interface for generating unique codes for links.
type IDGenerator interface {
	Generate() (string, error)
}

// Service is the domain's entry point.
type Service struct {
	store LinkStore
	idgen IDGenerator
}

// NewService wires the domain with its dependencies.
func NewService(store LinkStore, idgen IDGenerator) *Service {
	return &Service{store: store, idgen: idgen}
}

// Create validates and normalizes the URL, then generates a unique code and
// persists the link. On a code collision it regenerates and retries up to
// maxCreateRetries — the collision is *detected* by the store, the retry *decided* here,
// because uniqueness is a system property, not the generator's.
func (s *Service) Create(ctx context.Context, rawURL string) (*Link, error) {
	normalized, err := normalizeURL(rawURL)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < maxCreateRetries; attempt++ {
		code, err := s.idgen.Generate()
		if err != nil {
			return nil, fmt.Errorf("generating code: %w", err)
		}

		link := &Link{Code: code, LongURL: normalized}
		err = s.store.Create(ctx, link)
		switch {
		case err == nil:
			return link, nil
		case errors.Is(err, ErrCodeExists):
			continue // collision — regenerate and try again
		default:
			return nil, fmt.Errorf("persisting link: %w", err)
		}
	}
	return nil, fmt.Errorf("could not generate a unique code after %d attempts", maxCreateRetries)
}

// Resolve looks up the code and returns the corresponding link, or ErrNotFound if it doesn't exist.
func (s *Service) Resolve(ctx context.Context, code string) (*Link, error) {
	return s.store.GetByCode(ctx, code)
}

// normalizeURL trims whitespace and ensures the URL is parseable and has an http or https scheme.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: url is empty", ErrInvalidURL)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	// Lowercase before checking "HTTP://"
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: scheme must be http or https", ErrInvalidURL)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: missing host", ErrInvalidURL)
	}
	u.Host = strings.ToLower(u.Host)

	return u.String(), nil
}
