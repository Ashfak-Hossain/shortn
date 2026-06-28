# Use Snowflake-style IDs for distributed code generation (0007)

**Status:** Accepted, 2026-06-13. The worker-id assignment mechanism described here
(explicit `WORKER_ID` env var, later derived from a Kubernetes pod ordinal via
`StatefulSet`) is **superseded by [ADR 0014](0014-worker-id-assignment.md)**, where each
API instance leases a unique worker id from Redis. The rest of this ADR — the
Snowflake + sqids decision — stands.

## Context

An earlier design used `RandomBase62`: generate 7 cryptographically random base62
characters per request, check the DB for a collision, retry if needed. That works fine
for a single instance — each request is independent and the keyspace (62⁷ ≈ 3.5 trillion)
makes collisions astronomically rare.

The API runs as multiple stateless instances behind a load balancer. The collision
picture doesn't change: random generation still works correctly across instances because
every call is independent. The problem is different. The system wants codes whose
uniqueness guarantee is structural rather than probabilistic, and it wants to avoid DB
round-trips on the create path for the collision check. The system must also not expose
codes that are sequentially guessable, which would let anyone enumerate all shortened
links by iterating `/0`, `/1`, `/2`, …

Four strategies were evaluated:

1. **Continue with `RandomBase62`** — already in place; collision check is a separate
   DB round-trip; codes are not guessable; correct under multiple instances.
2. **DB auto-increment → base62-encode** — one canonical counter; zero collision risk;
   but sequential and enumerable; serialises all creates through one DB row; single point
   of failure.
3. **Snowflake IDs** — each instance generates 64-bit IDs from (timestamp_ms + worker ID
   + per-ms sequence) with no coordination; base62-encode for the public code; roughly
   time-sortable; non-sequential when base62-encoded; no DB round-trip.
4. **Pre-generated key pool (KGS)** — background worker fills a table with random codes;
   instances pop one atomically via `DELETE … FOR UPDATE SKIP LOCKED`; fully decouples
   generation from the request path; most complex to operate.

The decision must also solve enumerability. Snowflake IDs are derived from a
monotonically increasing integer, so base62-encoding a raw Snowflake is not enough: a
determined observer could reconstruct the sequence. A reversible shuffle of the integer
is needed before it becomes a public code.

## Decision

A Snowflake-style ID generator lives in `internal/idgen`:

- 64-bit integer layout: 1 unused sign bit | 41-bit epoch-relative millisecond timestamp
  | 10-bit worker ID | 12-bit per-ms sequence.
- Worker ID is assigned via the `WORKER_ID` environment variable (integer 0–1023);
  startup fails fast if it is missing or out of range. (This assignment mechanism is
  superseded by ADR 0014 — see Status.)
- The raw uint64 is encoded with sqids (`github.com/sqids/sqids-go`) using a shuffled
  base62 alphabet supplied via `SQIDS_ALPHABET`. Sqids produces a short, URL-safe,
  non-sequential string that is reversible given the same alphabet (useful for debugging)
  but not guessable without it. The alphabet must never change after codes have been
  issued — changing it makes every existing code unresolvable.
- On backward clock (`time.Now().UnixMilli()` < last observed ms), `Generate()` returns
  an error rather than silently risking a duplicate.
- `SnowflakeGenerator` implements the `shortener.IDGenerator` interface
  (`Generate() (string, error)`). Only `cmd/api/main.go` changes — the domain and HTTP
  layers are untouched.

## Alternatives considered

- **Continue with `RandomBase62`** — rejected because it retains a DB collision-check
  round-trip on the create path and offers no structural uniqueness guarantee. Correct,
  but it leaves the read of the create path coupled to the DB.

- **DB auto-increment → base62** — rejected because it is sequential (enumerable),
  serialises writes through one DB row (bottleneck under horizontal scale), and is a
  single point of failure. A finite-field scramble on top would fix enumerability but not
  the bottleneck.

- **Pre-generated KGS** — rejected as the primary strategy because it requires a
  background worker process, a `key_pool` table, and careful handling of pool exhaustion.
  It remains available as an optional track and is the correct huge-scale answer. If
  built, it would supersede this ADR.

- **Redis `INCR` for worker ID self-assignment** — originally rejected in favour of the
  explicit env var, because self-assignment added a startup network dependency and extra
  Redis logic. This tradeoff was later reversed: ADR 0014 adopts a Redis lease for worker
  ids so that horizontal scaling is collision-safe without manual per-instance
  configuration.

## Consequences

**Good:**

- No network round-trip for ID generation — `Generate()` is pure CPU.
- Structural uniqueness guarantee (within one worker ID, per ms): collisions are
  impossible without clock regression or worker-ID collision, both of which are explicitly
  guarded.
- Public codes are non-sequential and non-guessable (sqids obfuscation with a secret).
- The domain (`shortener.Service`) and HTTP layer are unchanged — only the wiring in
  `main.go` swaps the concrete type, because both sit behind the `IDGenerator` interface.

**Trade-offs / costs:**

- Clock skew is a real failure mode. If a VM's clock is corrected backward, the generator
  stalls (refuses to emit IDs) until the clock catches up. This is safe but observable.
  Mitigation: monitor for `ErrClockRegressed` and alert; NTP keeps backward jumps rare
  and small.
- Worker ID requires uniqueness across instances. Two instances with the same `WORKER_ID`
  at the same millisecond produce colliding raw IDs (sqids obfuscation does not change the
  underlying uniqueness property — it only changes the representation). ADR 0014 removes
  the manual env-var discipline by leasing the id from Redis.
- sqids adds a dependency and a secret that must be managed (via env/Secret). The secret
  must never change after codes have been issued, or existing codes become unresolvable.
- Codes are longer than 7 chars. A base62-encoded uint64 is at most 11 chars; sqids adds
  a small constant overhead. Acceptable — codes are still short and URL-safe.
