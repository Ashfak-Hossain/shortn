package cache

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestClient spins up an in-process fake Redis and returns a Client wired to it.
// miniredis runs the real go-redis code path with no Docker; t.Cleanup tears it
// down when the test ends.
func newTestClient(t *testing.T) (*Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	return New(rdb), mr
}

// TestClient_SetGet verifies a value written with Set round-trips back via Get.
func TestClient_SetGet(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	if err := c.Set(ctx, "k", "v", 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, found, err := c.Get(ctx, "k")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !found {
		t.Fatal("Get() found = false, want true")
	}
	if got != "v" {
		t.Errorf("Get() value = %q, want %q", got, "v")
	}
}

// TestClient_GetMiss verifies a missing key is a miss (found = false), NOT an
// error — the exact distinction the wrapper exists to make.
func TestClient_GetMiss(t *testing.T) {
	c, _ := newTestClient(t)

	got, found, err := c.Get(context.Background(), "absent")
	if err != nil {
		t.Fatalf("Get() on missing key error = %v, want nil", err)
	}
	if found {
		t.Error("Get() found = true, want false for a missing key")
	}
	if got != "" {
		t.Errorf("Get() value = %q, want empty string", got)
	}
}

// TestClient_Del verifies Del removes a key so a subsequent Get misses.
func TestClient_Del(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	_ = c.Set(ctx, "k", "v", 0)
	if err := c.Del(ctx, "k"); err != nil {
		t.Fatalf("Del() error = %v", err)
	}

	if _, found, _ := c.Get(ctx, "k"); found {
		t.Error("key still present after Del()")
	}
}
