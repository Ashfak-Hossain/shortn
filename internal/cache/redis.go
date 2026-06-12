// Package cache provides a Redis-backed caching layer for short-code lookups.
// It isolates all Redis I/O behind a small interface so the rest of the
// application never imports the Redis driver directly.
package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client holds a Redis connection and exposes the Get, Set, and Del
// operations the caching layer needs. not accessible outside the package
type Client struct {
	rdb *redis.Client
}

// New returns a Client backed by the given redis connection.
func New(rdb *redis.Client) *Client {
	return &Client{rdb: rdb}
}

// Get returns the value at key, respecting ctx cancellation. The boolean
// reports whether the key was found: a miss returns ("", false, nil), since
// a missing key is normal, not an error. A non-nil error means Redis itself
// failed.
func (c *Client) Get(ctx context.Context, key string) (string, bool, error) {
	val, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

// Set stores value at key with the given time-to-live, respecting ctx
// cancellation. Returns an error if the Redis command fails.
func (c *Client) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// Del removes key, respecting ctx cancellation. Deleting a missing key is a
// no-op, not an error.
func (c *Client) Del(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, key).Err()
}
