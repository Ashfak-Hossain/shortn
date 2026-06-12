// Package shortener implements the core domain logic of the URL shortener.
// It encapsulates the primary business rules and entities, remaining strictly
// isolated from HTTP delivery mechanisms and specific database implementations.
package shortener

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// We limit the number of retry attempts during link creation to prevent
// infinite loops in the event of high collision rates or underlying database issues.
const maxCreateRetries = 5

// Domain sentinel errors allow the service to signal specific failure states
// without leaking underlying implementation details (like SQL or network errors)
// to the external caller.
var (
	ErrNotFound   = errors.New("link not found")
	ErrCodeExists = errors.New("code already exists")
	ErrInvalidURL = errors.New("invalid url")
)

// Link represents a single URL mapping. It is the core business entity
// passed between the HTTP, Domain, and Persistence layers.
type Link struct {
	ID         int64
	Code       string
	LongURL    string
	CreatedAt  time.Time
	ExpiresAt  *time.Time // We use a pointer to represent a missing timestamp 'nil', which maps cleanly to a NULL in SQL.
	ClickCount int64
}

// LinkStore defines the persistence contract for links.
// By relying on this interface, the domain remains entirely agnostic to
// whether the data lives in Postgres, Redis, or memory.
type LinkStore interface {
	// Create persists a new link to the underlying datastore.
	Create(ctx context.Context, link *Link) error
	// GetByCode retrieves a link by its unique identifier.
	GetByCode(ctx context.Context, code string) (*Link, error)
}

// IDGenerator defines the contract for producing unique identifiers.
// Abstracting this allows to easily inject deterministic generators during testing.
type IDGenerator interface {
	Generate() (string, error)
}

// Service is the primary entry point for the domain.
// It orchestrates interactions between the generator and the data store.
type Service struct {
	store LinkStore
	idgen IDGenerator
}

// NewService initializes a new domain service with its required dependencies.
func NewService(store LinkStore, idgen IDGenerator) *Service {
	return &Service{store: store, idgen: idgen}
}

// Create validates a raw URL, assigns it a unique short code, and persists it.
// If a randomly generated code already exists in the system, it will seamlessly
// regenerate a new code and retry up to maxCreateRetries.
func (s *Service) Create(ctx context.Context, rawURL string) (*Link, error) {
	// We sanitize and validate the input immediately to fail fast and
	// avoid hitting the database with malformed data.
	normalized, err := normalizeURL(rawURL)
	if err != nil {
		return nil, err
	}

	// We use a bounded loop to handle random code collisions. The actual uniqueness
	// constraint is enforced by the database layer, but the retry policy is a
	// business rule dictated here.
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
			// We encountered a collision. We silently continue the loop to
			// regenerate a new code and try again.
			continue
		default:
			// We encountered a severe, non-recoverable database or network error.
			return nil, fmt.Errorf("persisting link: %w", err)
		}
	}
	return nil, fmt.Errorf("could not generate a unique code after %d attempts", maxCreateRetries)
}

// Resolve retrieves the destination URL mapping for a given short code.
// It returns ErrNotFound if the code does not exist in the system.
func (s *Service) Resolve(ctx context.Context, code string) (*Link, error) {
	return s.store.GetByCode(ctx, code)
}

// normalizeURL trims whitespace, forces a valid structure, and ensures the
// destination utilizes a secure HTTP/HTTPS protocol.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: url is empty", ErrInvalidURL)
	}

	// We utilize the standard library's URL parser to ensure the string
	// is structurally valid before manipulate it.
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	// We explicitly lower-case the scheme before evaluation to handle
	// valid but improperly cased inputs (e.g., HTTP://example.com).
	u.Scheme = strings.ToLower(u.Scheme)

	// Security Constraint: We strictly enforce HTTP/HTTPS to prevent users
	// from shortening malicious URI schemes like 'javascript:' or 'file:',
	// which could be exploited for Cross-Site Scripting (XSS) attacks.
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: scheme must be http or https", ErrInvalidURL)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: missing host", ErrInvalidURL)
	}

	// We normalize the host to lowercase to ensure consistency in the database
	// and prevent duplicate logical entries stored under different casings.
	u.Host = strings.ToLower(u.Host)

	return u.String(), nil
}
