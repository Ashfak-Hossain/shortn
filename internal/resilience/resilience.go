// Package resilience wraps a shortener.LinkStore with a circuit breaker so a
// failing Postgres fails fast (shedding load) instead of making every request
// wait out the timeout and pile up connections.
package resilience

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/sony/gobreaker"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

// Breaker tuning: trip after a short run of consecutive failures, then probe for
// recovery a few seconds later — long enough to stop hammering a dead Postgres,
// short enough to recover quickly once it's back.
const (
	breakerMaxConsecFails = 5
	breakerCooldown       = 5 * time.Second // open → half-open wait
	breakerHalfOpenProbes = 1               // trial requests allowed while half-open
)

// ResilientStore decorates a shortener.LinkStore with a shared Postgres circuit
// breaker. It is itself a LinkStore, so the cache and domain can't tell it apart.
type ResilientStore struct {
	next shortener.LinkStore
	cb   *gobreaker.CircuitBreaker
	log  *slog.Logger
}

var _ shortener.LinkStore = (*ResilientStore)(nil)

// NewResilientStore wraps next with a circuit breaker named "postgres".
func NewResilientStore(next shortener.LinkStore, log *slog.Logger) *ResilientStore {
	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "postgres",
		MaxRequests: breakerHalfOpenProbes,
		Timeout:     breakerCooldown,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.ConsecutiveFailures >= breakerMaxConsecFails
		},
		IsSuccessful: func(err error) bool {
			// "not found" / "code exists" mean Postgres answered fine — they are
			// normal results, not faults, so they must NOT count toward tripping.
			return err == nil || isExpected(err)
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			log.Warn("circuit breaker state changed", "breaker", name, "from", from.String(), "to", to.String())
		},
	})
	return &ResilientStore{next: next, cb: cb, log: log}
}

// isExpected reports whether err is a normal domain outcome rather than an
// infrastructure failure, so a flood of 404s can't open the circuit.
func isExpected(err error) bool {
	return errors.Is(err, shortener.ErrNotFound) || errors.Is(err, shortener.ErrCodeExists)
}

// execute runs op through the breaker and translates the breaker's open-state
// errors into shortener.ErrUnavailable, so the HTTP layer can answer 503 without
// importing gobreaker.
func (s *ResilientStore) execute(op func() (any, error)) (any, error) {
	res, err := s.cb.Execute(op)
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		return nil, shortener.ErrUnavailable
	}
	return res, err
}

func (s *ResilientStore) Create(ctx context.Context, link *shortener.Link) error {
	_, err := s.execute(func() (any, error) {
		return nil, s.next.Create(ctx, link)
	})
	return err
}

func (s *ResilientStore) GetByCode(ctx context.Context, code string) (*shortener.Link, error) {
	res, err := s.execute(func() (any, error) {
		return s.next.GetByCode(ctx, code)
	})
	if err != nil {
		return nil, err
	}
	return res.(*shortener.Link), nil
}

func (s *ResilientStore) GetStats(ctx context.Context, code string) (shortener.Stats, error) {
	res, err := s.execute(func() (any, error) {
		return s.next.GetStats(ctx, code)
	})
	if err != nil {
		return shortener.Stats{}, err
	}
	return res.(shortener.Stats), nil
}
