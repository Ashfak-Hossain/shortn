// Package resilience wraps a shortener.LinkStore with a circuit breaker so a
// failing Postgres fails fast (shedding load) instead of making every request
// wait out the timeout and pile up connections.
package resilience

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/sony/gobreaker"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

const (
	breakerMaxConsecFails = 5               // 5 failure in a row -> breaker open
	breakerCooldown       = 5 * time.Second // open → half-open wait 5s
	breakerHalfOpenProbes = 1               // 1 trial requests allowed while half-open
)

// retry start with ~20ms wait, grows uoto ~300ms before giveup
const (
	retryInitialInterval = 20 * time.Millisecond
	retryMaxElapsed      = 300 * time.Millisecond
)

// ResilientStore decorates a [shortener.LinkStore] with a shared Postgres circuit
// breaker. It is itself a LinkStore, so the cache and domain can't tell it apart.
type ResilientStore struct {
	next shortener.LinkStore       // real store (postgress)
	cb   *gobreaker.CircuitBreaker // the circuit breaker to call
	log  *slog.Logger
}

// Compile-time check that *ResilientStore satisfies shortener.LinkStore
var _ shortener.LinkStore = (*ResilientStore)(nil)

// NewResilientStore wraps next with a circuit breaker named "postgres".
func NewResilientStore(next shortener.LinkStore, log *slog.Logger) *ResilientStore {
	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "postgres",
		MaxRequests: breakerHalfOpenProbes,
		Timeout:     breakerCooldown,
		ReadyToTrip: func(c gobreaker.Counts) bool { // this funcs called after every failure
			return c.ConsecutiveFailures >= breakerMaxConsecFails
		},
		IsSuccessful: func(err error) bool {
			// "not found" is not an error and "code exists" means db is in good condition
			return err == nil || isExpected(err)
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			log.Warn("circuit breaker state changed", "breaker", name, "from", from.String(), "to", to.String())
		},
	})
	return &ResilientStore{next: next, cb: cb, log: log}
}

// isExpected returns if an error is acceptable or not. "not-found" and "code existed" is
// not counted as error here.
func isExpected(err error) bool {
	return errors.Is(err, shortener.ErrNotFound) || errors.Is(err, shortener.ErrCodeExists)
}

// execute runs every db call goes through the breaker. If the breaker is open or req more than max request
// it returns [shortener.ErrUnavailable]
func (s *ResilientStore) execute(op func() (any, error)) (any, error) {
	res, err := s.cb.Execute(op)
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		return nil, shortener.ErrUnavailable
	}
	return res, err
}

// Create persists a new link through the breaker. Writes are NOT retried: if a
// retried create's first attempt had actually succeeded, the retry would insert
// a duplicate link.
func (s *ResilientStore) Create(ctx context.Context, link *shortener.Link) error {
	_, err := s.execute(func() (any, error) {
		return nil, s.next.Create(ctx, link)
	})
	return err
}

// GetByCode reads a link. the breaker fails fast when
// Postgres is down, and retryRead gives a transient blip a few quick retries.
// The nested closures inside-out — hit Postgres, wrapped in retry, wrapped
// in the breaker.
func (s *ResilientStore) GetByCode(ctx context.Context, code string) (*shortener.Link, error) {
	res, err := s.execute(func() (any, error) { // breaker
		return s.retryRead(ctx, func() (any, error) { // retry
			return s.next.GetByCode(ctx, code) // main db call
		})
	})
	if err != nil {
		return nil, err
	}
	return res.(*shortener.Link), nil
}

// GetStats reads click analytics with the breaker and retry protection
func (s *ResilientStore) GetStats(ctx context.Context, code string) (shortener.Stats, error) {
	res, err := s.execute(func() (any, error) { // breaker
		return s.retryRead(ctx, func() (any, error) { // retry
			return s.next.GetStats(ctx, code) // db call
		})
	})
	if err != nil {
		return shortener.Stats{}, err
	}
	return res.(shortener.Stats), nil
}

// List reads a page of links with the breaker and retry protection — it's a read,
// so a transient blip gets the same few quick retries as GetByCode.
func (s *ResilientStore) List(ctx context.Context, limit, offset int) ([]*shortener.Link, error) {
	res, err := s.execute(func() (any, error) { // breaker
		return s.retryRead(ctx, func() (any, error) { // retry
			return s.next.List(ctx, limit, offset) // db call
		})
	})
	if err != nil {
		return nil, err
	}
	return res.([]*shortener.Link), nil
}

// Delete removes a link through the breaker. Like Create, writes are NOT retried:
// a retry whose first attempt actually succeeded would see ErrNotFound and report
// a spurious miss. ErrNotFound is "expected" (isExpected), so deleting a missing
// code never trips the breaker.
func (s *ResilientStore) Delete(ctx context.Context, code string) error {
	_, err := s.execute(func() (any, error) {
		return nil, s.next.Delete(ctx, code)
	})
	return err
}

// retryRead runs op with exponential backoff + jitter, bounded by both
// retryMaxElapsed and the request context. A domain outcome like ErrNotFound is
// treated as permanent — a clean miss is final, not a transient fault to retry.
func (s *ResilientStore) retryRead(ctx context.Context, op func() (any, error)) (any, error) {
	var result any
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = retryInitialInterval // 20ms
	bo.MaxElapsedTime = retryMaxElapsed       // 300ms

	err := backoff.Retry(func() error {
		r, opErr := op()
		switch {
		case opErr == nil:
			result = r
			return nil
		case isExpected(opErr):
			return backoff.Permanent(opErr) // not transient — stop immediately
		default:
			return opErr // transient → back off and retry
		}
	}, backoff.WithContext(bo, ctx))

	return result, err
}
