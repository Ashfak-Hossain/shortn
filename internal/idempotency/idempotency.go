// Package idempotency records the short code a given Idempotency-Key produced,
// in Redis, so a client that retries a create gets the same link back instead of
// a duplicate.
package idempotency

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store maps an Idempotency-Key to the code it created, with a TTL covering a
// client-retry window.
type Store struct {
	rdb *redis.Client
	ttl time.Duration
}

// New returns a Store backed by the given Redis client.
func New(rdb *redis.Client, ttl time.Duration) *Store {
	return &Store{rdb: rdb, ttl: ttl}
}

// Get returns the code previously recorded for key. found is false (with no
// error) when the key is unused — the normal first-request case.
func (s *Store) Get(ctx context.Context, key string) (code string, found bool, err error) {
	code, err = s.rdb.Get(ctx, redisKey(key)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return code, true, nil
}

// Set records that key produced code, but only if the key isn't already taken
// (SET NX). won is false when another request claimed the key first — the caller
// should then return that winner's link.
func (s *Store) Set(ctx context.Context, key string, code string) (won bool, err error) {
	return s.rdb.SetNX(ctx, redisKey(key), code, s.ttl).Result()
}

// redisKey namespaces idempotency keys so they never collide with cached links
// (`link:`) or rate-limit counters (`ratelimit:`) in the same Redis.
func redisKey(key string) string {
	return "idempo:" + key
}
