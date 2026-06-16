package idempotency

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestStore spins up an in-process fake Redis (no Docker) and returns a Store
// wired to it; t.Cleanup tears it down when the test ends.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	return New(rdb, time.Hour)
}

// TestFirstWriterWins is the core idempotency guarantee: the first Set for a key
// wins, a later Set with the same key is rejected (won == false), and Get returns
// the original code — so a retried create returns the same link.
func TestFirstWriterWins(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	won, err := s.Set(ctx, "key-1", "codeA")
	if err != nil || !won {
		t.Fatalf("first Set: won=%v err=%v, want won=true", won, err)
	}
	won, err = s.Set(ctx, "key-1", "codeB")
	if err != nil || won {
		t.Fatalf("second Set: won=%v err=%v, want won=false", won, err)
	}

	code, found, err := s.Get(ctx, "key-1")
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v, want found=true", found, err)
	}
	if code != "codeA" {
		t.Fatalf("Get returned %q, want the first writer's code %q", code, "codeA")
	}
}

// TestGetUnknownKey verifies an unused key reports not-found without an error —
// the normal first-request case.
func TestGetUnknownKey(t *testing.T) {
	s := newTestStore(t)
	if _, found, err := s.Get(context.Background(), "never-set"); err != nil || found {
		t.Fatalf("Get unused key: found=%v err=%v, want found=false err=nil", found, err)
	}
}

// TestConcurrentSameKeyHasOneWinner fires many simultaneous Sets for one key with
// distinct codes and asserts exactly one wins — the atomic SET NX guarantee that
// stops a race from creating duplicate links.
func TestConcurrentSameKeyHasOneWinner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const n = 50
	var wg sync.WaitGroup
	wins := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			won, err := s.Set(ctx, "race-key", "code-"+strconv.Itoa(i))
			if err != nil {
				t.Errorf("Set error: %v", err)
				return
			}
			wins[i] = won
		}(i)
	}
	wg.Wait()

	winners := 0
	for _, w := range wins {
		if w {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("got %d winners, want exactly 1", winners)
	}
}
