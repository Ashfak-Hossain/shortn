package cache

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// tombstone marks a code as known-absent in the cache. A real cached value is
// always a normalized http(s) URL, so a leading NUL byte can never collide with one.
const tombstone = "\x00notfound"

// negativeTTL is deliberately MUCH shorter than the positive TTL. A "does not
// exist" answer must expire quickly so a code created moments later becomes
// visible soon, and so a scanner can't pin many negative entries in memory.
const negativeTTL = 30 * time.Second

// meterName scopes this package's metrics in the OTel meter registry.
const meterName = "github.com/Ashfak-Hossain/shortn/internal/cache"

// CachingStore decorates a shortener.LinkStore with a read-through Redis cache.
// It is itself a shortener.LinkStore, so the domain service cannot tell whether
// it was handed the raw Postgres store or this cache-wrapped one.
type CachingStore struct {
	next     shortener.LinkStore // the wrapped store (Postgres) — the source of truth
	cache    *Client             // redis client
	ttl      time.Duration       // expiry for cache entries (real links)
	log      *slog.Logger        // non-fatal cache failures only
	group    singleflight.Group  // collapses concurrent misses for the same code into one DB load
	requests metric.Int64Counter // cache_requests_total{result="hit|miss"}
}

// Compile-time assertion that *CachingStore satisfies LinkStore.
var _ shortener.LinkStore = (*CachingStore)(nil)

// NewCachingStore wraps next with a Redis read-through cache.
func NewCachingStore(next shortener.LinkStore, cache *Client, ttl time.Duration, log *slog.Logger) *CachingStore {
	// An OTel counter, not a raw Prometheus one: the Prometheus registry lives in
	// internal/observability, and the global MeterProvider bridges this to it,
	// surfacing at /metrics as cache_requests_total{result="..."}. We keep hit and
	// miss as a counter PAIR and divide in PromQL — a ratio can't be averaged, so
	// it has to be computed at query time, never stored.
	requests, err := otel.Meter(meterName).Int64Counter(
		"cache.requests",
		metric.WithDescription("Cache lookups by outcome (hit or miss)."),
	)
	if err != nil {
		// Int64Counter hands back a usable no-op even on error, so this only logs to
		// flag a bad instrument name rather than nil-guarding every call site.
		log.Warn("failed to create cache.requests counter", "err", err)
	}
	return &CachingStore{next: next, cache: cache, ttl: ttl, log: log, requests: requests}
}

// Create implements [shortener.LinkStore].
// Create delegates straight to the wrapped store. A freshly generated random
// code has nothing cached yet, so there is no cache entry to write or invalidate.
func (s *CachingStore) Create(ctx context.Context, link *shortener.Link) error {
	return s.next.Create(ctx, link)
}

// GetStats implements [shortener.LinkStore]. Aggregate click stats aren't cached —
// they change on every click and aren't on the redirect hot path — so this
// delegates straight to the wrapped store.
func (s *CachingStore) GetStats(ctx context.Context, code string) (shortener.Stats, error) {
	return s.next.GetStats(ctx, code)
}

// List implements [shortener.LinkStore]. The link list isn't cached — it changes
// on every create and delete and isn't on the redirect hot path — so this
// delegates straight to the wrapped store.
func (s *CachingStore) List(ctx context.Context, limit, offset int) ([]*shortener.Link, error) {
	return s.next.List(ctx, limit, offset)
}

// Delete implements [shortener.LinkStore]. It removes the row from the source of
// truth first, then drops the cached redirect so a deleted code stops resolving
// at once instead of lingering until its TTL. A delete of a missing code returns
// ErrNotFound from the store and never touches the cache.
func (s *CachingStore) Delete(ctx context.Context, code string) error {
	if err := s.next.Delete(ctx, code); err != nil {
		return err
	}
	// Best-effort: Invalidate logs and returns any Redis error, but the entry's TTL
	// is the backstop, so a failure here must not make Delete fail.
	_ = s.Invalidate(ctx, code)
	return nil
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
		s.requests.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "hit")))
		return fromCached(code, val)
	}
	// A clean miss or a Redis error that forced a fallback both mean the cache
	// didn't answer — both count as a miss.
	s.requests.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "miss")))

	v, err, _ := s.group.Do(code, func() (any, error) {
		return s.loadAndCache(ctx, code) // load from db and populate cache
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

// Invalidate removes a code's cached entry so the next read repopulates from the
// store. This is the seam a future link edit/delete handler calls to keep Redis
// consistent with Postgres. No caller exists yet. Fail open: a Redis error is logged
// and returned, but the caller safely ignore it because the entry's TTL is the backstop.
func (s *CachingStore) Invalidate(ctx context.Context, code string) error {
	if err := s.cache.Del(ctx, key(code)); err != nil {
		s.log.Warn("cache invalidate failed", "code", code, "err", err)
		return err
	}
	return nil
}

// key namespaces cache keys so links never collide with other data Redis might
// hold later (rate-limit counters, sessions, etc.). Value at link:{code} is the long URL.
func key(code string) string {
	return "link:" + code
}
