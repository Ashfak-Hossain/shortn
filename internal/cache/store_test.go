package cache

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

// fakeStore is an in-memory LinkStore that COUNTS GetByCode calls (so we can prove
// the cache prevents store hits) and can DELAY each read (so we can open a
// concurrency window for the singleflight test).
type fakeStore struct {
	mu     sync.Mutex
	byCode map[string]*shortener.Link
	calls  atomic.Int32
	delay  time.Duration
}

func newFakeStore() *fakeStore {
	return &fakeStore{byCode: make(map[string]*shortener.Link)}
}

func (f *fakeStore) Create(_ context.Context, link *shortener.Link) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byCode[link.Code] = link
	return nil
}

func (f *fakeStore) GetByCode(_ context.Context, code string) (*shortener.Link, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	link, ok := f.byCode[code]
	if !ok {
		return nil, shortener.ErrNotFound
	}
	return link, nil
}

// newTestStore wires a CachingStore over the fake store, backed by in-process Redis.
// It returns the decorator and the miniredis handle (so a test can inspect cached
// keys or kill Redis to exercise fail-open).
func newTestStore(t *testing.T, next shortener.LinkStore, ttl time.Duration) (*CachingStore, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil)) // keep test output quiet
	return NewCachingStore(next, New(rdb), ttl, log), mr
}

// TestCachingStore_HitSkipsStore proves a second lookup is served from Redis and
// never reaches the underlying store.
func TestCachingStore_HitSkipsStore(t *testing.T) {
	next := newFakeStore()
	next.byCode["abc"] = &shortener.Link{Code: "abc", LongURL: "https://example.com"}
	cs, _ := newTestStore(t, next, time.Hour)
	ctx := context.Background()

	// First lookup: a miss, so it reaches the store and populates the cache.
	if _, err := cs.GetByCode(ctx, "abc"); err != nil {
		t.Fatalf("first GetByCode() error = %v", err)
	}
	if got := next.calls.Load(); got != 1 {
		t.Fatalf("after first lookup store calls = %d, want 1", got)
	}

	// Second lookup: must be a cache hit — store calls must NOT increase.
	link, err := cs.GetByCode(ctx, "abc")
	if err != nil {
		t.Fatalf("second GetByCode() error = %v", err)
	}
	if got := next.calls.Load(); got != 1 {
		t.Errorf("after cache hit store calls = %d, want still 1", got)
	}
	if link.LongURL != "https://example.com" {
		t.Errorf("LongURL = %q, want %q", link.LongURL, "https://example.com")
	}
}

// TestCachingStore_MissPopulatesCache verifies a miss writes the URL into Redis
// under link:{code} with the positive TTL.
func TestCachingStore_MissPopulatesCache(t *testing.T) {
	next := newFakeStore()
	next.byCode["abc"] = &shortener.Link{Code: "abc", LongURL: "https://example.com"}
	cs, mr := newTestStore(t, next, time.Hour)

	if _, err := cs.GetByCode(context.Background(), "abc"); err != nil {
		t.Fatalf("GetByCode() error = %v", err)
	}

	got, err := mr.Get("link:abc")
	if err != nil {
		t.Fatalf("expected key link:abc in redis: %v", err)
	}
	if got != "https://example.com" {
		t.Errorf("cached value = %q, want %q", got, "https://example.com")
	}
	if ttl := mr.TTL("link:abc"); ttl != time.Hour { // miniredis exposes the set TTL
		t.Errorf("cached TTL = %v, want %v", ttl, time.Hour)
	}
}

// TestCachingStore_NegativeCaching verifies a not-found result is cached as the
// tombstone (short TTL) and a repeat lookup is served WITHOUT hitting the store.
func TestCachingStore_NegativeCaching(t *testing.T) {
	next := newFakeStore() // empty: every code is unknown
	cs, mr := newTestStore(t, next, time.Hour)
	ctx := context.Background()

	if _, err := cs.GetByCode(ctx, "nope"); !errors.Is(err, shortener.ErrNotFound) {
		t.Fatalf("GetByCode() error = %v, want ErrNotFound", err)
	}
	if got := next.calls.Load(); got != 1 {
		t.Fatalf("store calls = %d, want 1", got)
	}

	if got, _ := mr.Get("link:nope"); got != tombstone {
		t.Errorf("cached value = %q, want tombstone", got)
	}
	if ttl := mr.TTL("link:nope"); ttl != negativeTTL {
		t.Errorf("negative TTL = %v, want %v", ttl, negativeTTL)
	}

	// Repeat lookup must be served from the negative cache — store calls unchanged.
	if _, err := cs.GetByCode(ctx, "nope"); !errors.Is(err, shortener.ErrNotFound) {
		t.Fatalf("second GetByCode() error = %v, want ErrNotFound", err)
	}
	if got := next.calls.Load(); got != 1 {
		t.Errorf("after negative cache hit store calls = %d, want still 1", got)
	}
}

// TestCachingStore_FailOpen proves that if Redis is down, redirects still resolve
// from the store instead of erroring.
func TestCachingStore_FailOpen(t *testing.T) {
	next := newFakeStore()
	next.byCode["abc"] = &shortener.Link{Code: "abc", LongURL: "https://example.com"}
	cs, mr := newTestStore(t, next, time.Hour)

	mr.Close() // simulate Redis going down

	link, err := cs.GetByCode(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetByCode() with redis down error = %v, want nil (fail open)", err)
	}
	if link.LongURL != "https://example.com" {
		t.Errorf("LongURL = %q, want %q", link.LongURL, "https://example.com")
	}
}

// TestCachingStore_SingleflightCollapsesMisses fires many concurrent lookups for
// the same cold code and proves the store was queried exactly once.
func TestCachingStore_SingleflightCollapsesMisses(t *testing.T) {
	next := newFakeStore()
	next.byCode["hot"] = &shortener.Link{Code: "hot", LongURL: "https://example.com"}
	next.delay = 50 * time.Millisecond // widen the window so the goroutines overlap
	cs, _ := newTestStore(t, next, time.Hour)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			link, err := cs.GetByCode(context.Background(), "hot")
			if err != nil || link.LongURL != "https://example.com" {
				t.Errorf("concurrent GetByCode() = (%v, %v)", link, err)
			}
		}()
	}
	wg.Wait()

	if got := next.calls.Load(); got != 1 {
		t.Errorf("store called %d times for %d concurrent misses, want 1 (singleflight)", got, n)
	}
}
