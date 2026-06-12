package cache

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

// tombstone marks a code as known-absent in the cache. A real cached value is
// always a normalized http(s) URL, so a leading NUL byte can never collide with one.
const tombstone = "\x00notfound"

// negativeTTL is deliberately MUCH shorter than the positive TTL. A "does not
// exist" answer must expire quickly so a code created moments later becomes
// visible soon, and so a scanner can't pin many negative entries in memory.
const negativeTTL = 30 * time.Second

// CachingStore decorates a shortener.LinkStore with a read-through Redis cache.
// It is itself a shortener.LinkStore, so the domain service cannot tell whether
// it was handed the raw Postgres store or this cache-wrapped one.
type CachingStore struct {
	next  shortener.LinkStore // the wrapped store (Postgres) — the source of truth
	cache *Client
	ttl   time.Duration
	log   *slog.Logger       // non-fatal cache failures only
	group singleflight.Group // collapses concurrent misses for the same code into one DB load
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
// GetByCode serves from Redis on a hit, and on a miss collapses concurrent
// lookups for the same code into a single store read via singleflight. Both
// real links and "not found" results are cached (the latter briefly). Every
// Redis error is non-fatal: we log and fall back to the source of truth.
func (s *CachingStore) GetByCode(ctx context.Context, code string) (*shortener.Link, error) {
	// Fast path: try the cache. A Redis failure here is non-fatal (fail open).
	if val, found, err := s.cache.Get(ctx, key(code)); err != nil {
		s.log.Warn("cache get failed; serving from store", "code", code, "err", err)
	} else if found {
		return fromCached(code, val)
	}

	// Miss (or Redis down): collapse all concurrent misses for THIS code into one
	// store load. The first goroutine ("leader") runs the func; the rest block and
	// share its result. group.Do keys on the code, so different codes don't block.
	v, err, _ := s.group.Do(code, func() (any, error) {
		return s.loadAndCache(ctx, code)
	})
	if err != nil {
		return nil, err
	}
	return v.(*shortener.Link), nil
}

// fromCached interprets a cached value: the tombstone means "known absent",
// anything else is a real URL.
func fromCached(code, val string) (*shortener.Link, error) {
	if val == tombstone {
		return nil, shortener.ErrNotFound
	}
	return &shortener.Link{Code: code, LongURL: val}, nil
}

// loadAndCache reads the source of truth and populates the cache. It runs inside
// singleflight, so a burst of concurrent misses for the same code executes it once.
func (s *CachingStore) loadAndCache(ctx context.Context, code string) (*shortener.Link, error) {
	link, err := s.next.GetByCode(ctx, code)
	if errors.Is(err, shortener.ErrNotFound) {
		// Negative caching: remember the absence briefly so repeated lookups of a
		// non-existent code stop hammering the DB.
		if err := s.cache.Set(ctx, key(code), tombstone, negativeTTL); err != nil {
			s.log.Warn("negative cache set failed", "code", code, "err", err)
		}
		return nil, shortener.ErrNotFound
	}
	if err != nil {
		return nil, err // a real store failure — don't cache it
	}
	// Positive caching.
	if err := s.cache.Set(ctx, key(code), link.LongURL, s.ttl); err != nil {
		s.log.Warn("cache set failed", "code", code, "err", err)
	}
	return link, nil
}

// key namespaces cache keys so links never collide with other data Redis might
// hold later (rate-limit counters, sessions, etc.). Value at link:{code} is the long URL.
func key(code string) string {
	return "link:" + code
}
