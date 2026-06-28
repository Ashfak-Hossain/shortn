# Resilience & reliability (0009)

**Status:** Accepted, 2026-06-16

## Context

`shortn` is distributed, but it must not hard-fail when a dependency misbehaves, and
it needs a defence against abuse. The resilience posture covers two threats: failing
dependencies (Postgres, Redis, Redpanda — down or, worse, hung) and abusive callers.
These are several related decisions; rather than one ADR each, this record captures
the resilience posture as a set.

The guiding principle: Postgres is the only hard dependency (no writes, no cold reads
without it). Redis and Redpanda are optimizations; their loss must degrade the
experience, never break correctness.

## Decision

1. **Timeouts, two layers.** A request-level deadline (`TimeoutMiddleware`, 2s)
   propagates via `context.Context` into every call, giving cancellation and a hard
   ceiling. On top of that, explicit go-redis client timeouts (`ReadTimeout`/`WriteTimeout`
   200ms, `DialTimeout`/`PoolTimeout` 300ms), because a `context` deadline alone does not
   bound a hung Redis connection — go-redis falls back to its 3s default `ReadTimeout`
   (see Consequences).

2. **Distributed rate limiting.** A Redis-backed token bucket (Lua, evaluated
   atomically; capacity 20, refill 10/s, per client IP via `X-Real-IP`) returning
   `429` + `Retry-After`. State lives in Redis so the limit is shared across all API
   instances. Fails open if Redis is unreachable.

3. **Circuit breaker on Postgres.** `sony/gobreaker`, wrapping the store between the
   cache and Postgres (so cache hits bypass it). Trips after 5 consecutive failures,
   5s open cooldown, 1 half-open probe. Domain results (`ErrNotFound`, `ErrCodeExists`)
   are not counted as failures. An open breaker surfaces as `shortener.ErrUnavailable`,
   which maps to HTTP `503`, keeping `internal/http` free of any `gobreaker` import.

4. **Retries, reads only.** `cenkalti/backoff/v4` (exponential + jitter, 20ms→300ms
   bound) on idempotent reads (`GetByCode`, `GetStats`). Writes are never retried.

5. **Idempotency keys.** An optional `Idempotency-Key` header dedupes `POST /api/links`
   via a Redis `SET NX` (`idempo:` prefix, 24h TTL). Treated as an HTTP-layer concern;
   the domain, store, and schema are untouched. Fails open.

6. **Liveness vs readiness.** `/healthz` stays dependency-free (liveness). `/readyz`
   checks only Postgres (readiness), deliberately not Redis, because a Redis outage is
   degraded-but-serving.

## Alternatives considered

- **Per-call `context.WithTimeout` on each Redis op instead of client timeouts** —
  built, then removed. go-redis didn't honour the per-call deadline on a hung
  connection, so it was redundant theatre over the client-level `ReadTimeout`.
- **In-process rate limiter** — rejected: with N instances the effective limit becomes
  N×. The counter must be shared state.
- **Fail _closed_ on Redis outage** (limiter/idempotency) — rejected: turns a cache
  outage into a total outage. We accept weaker guarantees during a Redis blip instead.
- **Circuit breaker on Redis too** — rejected: the cache already fails open, so a
  breaker there adds little. Postgres is where fail-fast matters.
- **Idempotency in Postgres (column or table)** — rejected for now: ripples through the
  `cache → resilient → postgres` decorator chain and needs a migration. Redis is
  lighter for a best-effort dedup window.
- **`/readyz` checking Redis** — rejected: would pull a still-serving instance out of
  the load balancer for a non-critical dependency.

## Consequences

**Good.** The system degrades gracefully and the behavior is documented and tested
(`make chaos` asserts the [runbook](../runbook.md) failure-mode table). A hung or down
dependency fails fast instead of exhausting goroutines and connections.

**Trade-offs / costs.**

- Rate limit and idempotency are unenforced during a Redis outage (fail-open). An
  nginx-level limit is the backstop.
- The breaker is per-instance, not shared like the rate limiter. This is correct: a
  breaker reflects this process's view of a dependency, whereas the limiter enforces a
  global ceiling and must share state.
- Idempotency has a rare concurrent-race orphan: two simultaneous same-key creates can
  leave one unreferenced link. Sequential retries, the real use case, are exact.
- The hung-vs-down lesson. "Fail open" is meaningless without a tight timeout to fail
  open quickly, and a `context` deadline does not reliably bound a client library on a
  hung connection; the library's own timeout knobs must be set. A hung dependency is
  worse than a down one: a down one refuses fast, a hung one ties up resources until
  something times out.
- `NewRouter` takes 8 parameters; a "group into a `Deps` struct" refactor is owed.
