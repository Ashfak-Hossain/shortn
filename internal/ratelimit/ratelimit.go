// Package ratelimit implements a distributed token-bucket limiter backed by
// Redis, so one shared limit is enforced across every API instance instead of
// each process allowing the full rate on its own.
package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// bucketScript is a token bucket evaluated atomically inside Redis. Doing the
// whole refill-check-take in one script is what makes it correct under
// concurrency: no other request can observe a half-updated bucket.
const bucketScript = `
-- KEYS[1] = bucket key   ARGV[1] = burst (capacity)   ARGV[2] = rps (refill/sec)
local burst = tonumber(ARGV[1])
local rps   = tonumber(ARGV[2])

-- Redis's own clock, so all API instances share one time source (no skew).
local t   = redis.call('TIME')
local now = tonumber(t[1]) + tonumber(t[2]) / 1000000

local data   = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts     = tonumber(data[2])
if tokens == nil then
  tokens = burst
  ts     = now
end

-- Refill for the time elapsed since this key was last touched.
tokens = math.min(burst, tokens + (now - ts) * rps)

local allowed  = 0
local retry_ms = 0
if tokens >= 1 then
  allowed = 1
  tokens  = tokens - 1
else
  retry_ms = math.ceil((1 - tokens) / rps * 1000)
end

redis.call('HSET', KEYS[1], 'tokens', tokens, 'ts', now)
-- Reclaim idle buckets once they'd be fully refilled anyway.
redis.call('PEXPIRE', KEYS[1], math.ceil(burst / rps * 1000) + 1000)

return { allowed, retry_ms }
`

// Limiter enforces a per-key token bucket in Redis. burst is the bucket
// capacity (largest momentary spike); rps is the steady refill rate.
type Limiter struct {
	rdb    *redis.Client // the shared Redis holding every distributed client's bucket
	script *redis.Script // compiled once; go-redis caches it in Redis so each Allow is one round-trip
	burst  int           // bucket capacity.The biggest instant spike one key may make before it empties
	rps    int           // refill rate (tokens/sec).use once the burst is spent
}

// New compiles the bucket script once and returns a Limiter.
func New(rdb *redis.Client, burst, rps int) *Limiter {
	return &Limiter{
		rdb:    rdb,
		script: redis.NewScript(bucketScript),
		burst:  burst,
		rps:    rps,
	}
}

// Allow takes one token for key. It reports whether the request is permitted
// and, when it isn't, how long until a token frees up. A non-nil error means
// Redis itself failed — the caller decides whether to fail open.
func (l *Limiter) Allow(ctx context.Context, key string) (allowed bool, retryAfter time.Duration, err error) {
	// The script returns {allowed, retry_ms}: res[0] is 1 when a token was taken,
	// res[1] is the wait in milliseconds until the next token frees up.
	res, err := l.script.Run(ctx, l.rdb, []string{key}, l.burst, l.rps).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	return res[0] == 1, time.Duration(res[1]) * time.Millisecond, nil
}
