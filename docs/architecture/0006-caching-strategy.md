# Caching strategy: cache-aside, TTL & fail-open (0006)

**Status:** Accepted, 2026-06-12

## Context

A URL shortener is overwhelmingly read-heavy: redirects (`GET /{code}`) outnumber
link creation by ~100:1 or more. Without a cache, every redirect hits Postgres
with a `SELECT … WHERE code = $1`. That works, but it puts the busiest path of the
whole system on the database, and the hottest links (a viral URL) concentrate that
load on a handful of rows. The cache takes the read path off Postgres for repeat
lookups without changing the domain logic and without letting the cache become a
new way for the service to fail.

Four forces shape the decision:

- **Read/write asymmetry** — optimizing reads dominates; writes are rare.
- **A cache is an optimization, not a dependency** — Redis being down must degrade
  performance, never correctness.
- **Hot-key concurrency** — when a popular entry expires, many requests miss at
  once and can stampede the database.
- **Layering discipline** — the domain (`shortener`) must not learn that Redis
  exists (see the dependency-inversion rationale in [ARCHITECTURE.md](../../ARCHITECTURE.md)).

## Decision

We add a Redis cache on the read path using the **cache-aside** pattern, wired as
a **decorator** that implements `shortener.LinkStore` and wraps the Postgres store
(`internal/cache`). Concretely:

- **Cache-aside reads.** `GetByCode` checks Redis first; on a miss it reads
  Postgres and populates Redis. Key format `link:{code}`, value = the long URL.
- **Positive TTL = 1 hour.** Long enough that hot links almost always hit; short
  enough that a missed invalidation self-heals within an hour.
- **Negative caching, TTL = 30s.** A not-found result is cached as a NUL-prefixed
  `tombstone` sentinel (`"\x00notfound"`, which a normalized `http(s)` URL can
  never equal) so scans of random codes don't turn every 404 into a DB query. The
  short TTL bounds how long a later-created code stays invisible.
- **Singleflight on the miss path.** Concurrent misses for the *same* code are
  collapsed (`golang.org/x/sync/singleflight`) into one store load; followers
  share the leader's result.
- **Fail open.** Every Redis error (get, set, del) is logged and ignored; the
  request proceeds against Postgres. At startup, an unreachable Redis is a
  `Warn`, not a fatal exit — unlike Postgres, which stays a hard dependency.
- **Invalidation seam.** `CachingStore.Invalidate(code)` deletes the key via the
  shared `key()` helper, ready for a future edit/delete handler. TTL is the
  backstop if it is ever missed.

The domain service and HTTP layer are unchanged: `main.go` injects the
cache-wrapped store into `shortener.NewService` in place of the raw store.

## Alternatives considered

- **Write-through cache** — rejected. Every write updates the cache synchronously,
  keeping it warm and consistent. But our writes are rare and a freshly created
  random code is never read before its creator gets the response, so warming on
  write buys nothing and adds a second system to every write. Cache-aside matches
  the read-heavy, write-rare shape.
- **Write-behind (write-back) cache** — rejected. The cache absorbs writes and
  flushes to Postgres asynchronously — strong write throughput, but it makes the
  cache authoritative and risks data loss if Redis dies before the flush.
  Unacceptable when Postgres is our source of truth.
- **Cache as a field on `shortener.Service`** (orchestrate cache-then-store inside
  `Resolve`) — rejected. It works, but the domain would then *know* a cache exists,
  leaking an infrastructure concern into the layer that is supposed to be pure I/O
  ignorance. The decorator keeps `shortener` and `http` untouched.
- **No TTL (cache forever, rely only on invalidation)** — rejected. Unbounded
  memory growth, and any missed invalidation produces permanently stale redirects.
  TTL is both the memory bound and the staleness safety net.
- **No negative caching** — rejected. Leaves the DB exposed to bots probing random
  codes; each probe is a guaranteed miss → DB round-trip.
- **No stampede protection** — rejected. A viral link expiring would let thousands
  of concurrent misses hit Postgres in the same instant — the "optimization"
  causing the outage it was meant to prevent.
- **Redis persistence (AOF/RDB volume)** — rejected for now. A cache is ephemeral
  by design; on restart it repopulates from Postgres on the next miss. Persisting
  it would only buy a warm cache after restart at the cost of stale entries
  surviving. No volume is mounted for Redis in compose.

## Consequences

### Good

- **Redirects skip Postgres on a hit** — proven by serving a cached code with
  Postgres stopped. The hot path comes off the database.
- **Correctness survives a Redis outage** — fail-open means a Redis failure
  degrades latency, never the redirect itself.
- **Stampede-safe** — one DB load per hot key per miss window, not thousands.
- **Abuse-resistant** — negative caching blocks random-code scanning from pounding
  the DB.
- **Domain stays pure** — `shortener` and `http` did not change; swapping or
  removing the cache is a one-line wiring change in `main.go`.

### Bad / trade-offs

- **Staleness window up to the TTL.** Until an edit/delete handler exists to call
  `Invalidate`, an edited link could serve the old URL for up to one hour. Bounded,
  but real; the TTL value is the knob.
- **A second moving part.** Redis is one more service to run, monitor, and reason
  about (e.g. eviction policy under memory pressure — deferred, not yet configured).
- **Eventual consistency by construction.** Cache-aside accepts a small consistency
  window in exchange for read performance; anything needing read-your-writes on an
  *edited* link must invalidate explicitly, not rely on the cache.
- **`Invalidate` has no caller yet.** It is a documented, tested seam ahead of need
  — intentional, but it is code paying rent before it earns it.
