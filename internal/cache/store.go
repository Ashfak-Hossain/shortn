package cache

import (
	"context"
	"log/slog"
	"time"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

// CachingStore decorates a shortener.LinkStore with a read-through Redis cache.
// It is itself a shortener.LinkStore, so the domain service cannot tell whether
// it was handed the raw Postgres store or this cache-wrapped one.
type CachingStore struct {
	next  shortener.LinkStore // the wrapped store (Postgres) — the source of truth
	cache *Client
	ttl   time.Duration
	log   *slog.Logger // non-fatal cache failures only
}

// Compile-time assertion that *CachingStore satisfies LinkStore.
var _ shortener.LinkStore = (*CachingStore)(nil)

// NewCachingStore wraps next with a Redis read-through cache.
func NewCachingStore(next shortener.LinkStore, cache *Client, ttl time.Duration, log *slog.Logger) *CachingStore {
	return &CachingStore{next: next, cache: cache, ttl: ttl, log: log}
}

// Create implements [shortener.LinkStore].
// Create delegates straight to the wrapped store. A freshly generated random
// code has nothing cached yet, so there is no cache entry to write or invalidate.
func (s *CachingStore) Create(ctx context.Context, link *shortener.Link) error {
	return s.next.Create(ctx, link)
}

// GetByCode implements [shortener.LinkStore].
// GetByCode serves from Redis on a hit and falls back to the wrapped store on a
// miss, populating the cache afterward. Every Redis error is non-fatal: we log
// it and serve from the source of truth. The cache can never break correctness.
func (s *CachingStore) GetByCode(ctx context.Context, code string) (*shortener.Link, error) {
	k := key(code)

	if url, found, err := s.cache.Get(ctx, k); err != nil {
		s.log.Warn("cache get failed; serving from store", "code", code, "err", err)
	} else if found {
		return &shortener.Link{Code: code, LongURL: url}, nil
	}

	link, err := s.next.GetByCode(ctx, code)
	if err != nil {
		return nil, err
	}

	if err := s.cache.Set(ctx, k, link.LongURL, s.ttl); err != nil {
		s.log.Warn("cache set failed", "code", code, "err", err)
	}

	return link, nil
}

// key namespaces cache keys so links never collide with other data Redis might
// hold later (rate-limit counters, sessions, etc.). Value at link:{code} is the long URL.
func key(code string) string {
	return "link:" + code
}
