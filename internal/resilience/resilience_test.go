package resilience

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

// fakeStore is a programmable shortener.LinkStore for exercising the breaker and
// retry logic without a real database. Each method delegates to a func field, so
// a test wires only the behavior it cares about.
type fakeStore struct {
	getByCode func(ctx context.Context, code string) (*shortener.Link, error)
	create    func(ctx context.Context, link *shortener.Link) error
	getStats  func(ctx context.Context, code string) (shortener.Stats, error)
}

func (f *fakeStore) GetByCode(ctx context.Context, code string) (*shortener.Link, error) {
	return f.getByCode(ctx, code)
}

func (f *fakeStore) Create(ctx context.Context, link *shortener.Link) error {
	return f.create(ctx, link)
}

func (f *fakeStore) GetStats(ctx context.Context, code string) (shortener.Stats, error) {
	return f.getStats(ctx, code)
}

// discardLogger silences the breaker's OnStateChange warnings during tests.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestBreakerOpensAndFailsFast drives exactly the trip threshold of consecutive
// failures, then asserts the next call fails fast with ErrUnavailable without
// reaching the wrapped store. Create is used because it isn't retried, so each
// call is a single store hit and the breaker trips deterministically.
func TestBreakerOpensAndFailsFast(t *testing.T) {
	dbErr := errors.New("connection refused")
	calls := 0
	store := &fakeStore{
		create: func(ctx context.Context, link *shortener.Link) error {
			calls++
			return dbErr
		},
	}
	rs := NewResilientStore(store, discardLogger())
	ctx := context.Background()

	// While the circuit is closed, each failure surfaces the raw infra error.
	for i := 0; i < breakerMaxConsecFails; i++ {
		if err := rs.Create(ctx, &shortener.Link{}); !errors.Is(err, dbErr) {
			t.Fatalf("attempt %d: got %v, want %v", i, err, dbErr)
		}
	}

	// The circuit is now open: the next call must short-circuit to ErrUnavailable
	// and leave the store untouched.
	before := calls
	if err := rs.Create(ctx, &shortener.Link{}); !errors.Is(err, shortener.ErrUnavailable) {
		t.Fatalf("breaker open: got %v, want ErrUnavailable", err)
	}
	if calls != before {
		t.Fatalf("store was called while breaker open: calls went %d -> %d", before, calls)
	}
}

// TestReadRetriesTransientThenSucceeds verifies a transient read failure is
// retried with backoff and the eventual success is returned to the caller.
func TestReadRetriesTransientThenSucceeds(t *testing.T) {
	want := &shortener.Link{Code: "abc", LongURL: "https://example.com"}
	calls := 0
	store := &fakeStore{
		getByCode: func(ctx context.Context, code string) (*shortener.Link, error) {
			calls++
			if calls < 3 {
				return nil, errors.New("transient")
			}
			return want, nil
		},
	}
	rs := NewResilientStore(store, discardLogger())

	got, err := rs.GetByCode(context.Background(), "abc")
	if err != nil {
		t.Fatalf("got err %v, want success after retries", err)
	}
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	if calls != 3 {
		t.Fatalf("store called %d times, want 3 (2 failures + 1 success)", calls)
	}
}

// TestNotFoundIsNotRetriedAndDoesNotTrip verifies that a clean miss returns
// immediately (no retries) and never counts toward opening the breaker — even
// across more calls than the trip threshold.
func TestNotFoundIsNotRetriedAndDoesNotTrip(t *testing.T) {
	calls := 0
	store := &fakeStore{
		getByCode: func(ctx context.Context, code string) (*shortener.Link, error) {
			calls++
			return nil, shortener.ErrNotFound
		},
	}
	rs := NewResilientStore(store, discardLogger())
	ctx := context.Background()

	const n = breakerMaxConsecFails + 5
	for i := 0; i < n; i++ {
		if _, err := rs.GetByCode(ctx, "missing"); !errors.Is(err, shortener.ErrNotFound) {
			t.Fatalf("attempt %d: got %v, want ErrNotFound", i, err)
		}
	}

	// Exactly one store call per request proves both properties: no retries (else
	// calls > n) and the breaker never opened (else later calls short-circuit and
	// calls < n).
	if calls != n {
		t.Fatalf("store called %d times, want %d (no retries, breaker stays closed)", calls, n)
	}
}
