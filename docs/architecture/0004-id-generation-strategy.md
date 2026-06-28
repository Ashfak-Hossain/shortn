# ID generation strategy (0004)

**Status:** Accepted, 2026-06-11

## Context

A URL shortener's core job is to mint a short, unique code for each long URL and resolve it back fast. The code-generation strategy is the defining design choice of the service. It dictates code length, predictability, collision behavior, and whether the service can scale to multiple writers.

Constraints:

- Codes must be URL-safe and short.
- Codes must be unique. Two links can never share a code.
- The implementation must be swappable, so a later move to multiple stateless API instances and a distributed ID scheme does not touch the domain or HTTP layers.

The four classic strategies:

1. Hash + truncate. SHA-256 the URL, take the first N chars, base62-encode. Deterministic and stateless, but truncation reintroduces collisions, and the same URL always maps to the same code (leaks that two users shortened the same link, and removes the freedom to expire or re-issue).
2. Random base62 + collision retry. Generate N random base62 chars, insert, retry on a unique-constraint violation. Unpredictable, simple, no shared state; costs one DB round-trip to confirm uniqueness.
3. Auto-increment ID to base62. Encode a monotonic counter. No collisions and the shortest possible codes, but the codes are sequential and enumerable (anyone can walk `/1`, `/2`, …) and a single counter breaks across multiple writer instances.
4. Pre-generated key pool / KGS. A background service fills a table with unused codes; instances pop from it. The textbook answer at very large scale, but operationally heavy.

## Decision

The service uses random base62 with collision-retry, behind an `IDGenerator` interface.

- The alphabet is base62 (`[0-9a-zA-Z]`), 62 symbols. Codes are 7 characters, so 62⁷ ≈ 3.5 trillion possible codes.
- Randomness comes from `crypto/rand` (not `math/rand`) so codes are unpredictable and cannot be enumerated from a seed.
- Uniqueness is enforced by the database: `links.code` has a `UNIQUE` constraint. On an insert collision (SQLSTATE `23505`), the domain service regenerates a code and retries, capped at 5 attempts.
- The generator is hidden behind the domain's `IDGenerator` interface (`Generate() (string, error)`); the concrete `RandomBase62` lives in `internal/idgen`. A distributed scheme (Snowflake) replaces it by swapping the implementation only — see [ADR 0007](0007-distributed-id-generation.md).

## Alternatives considered

- Hash + truncate — rejected. Truncation collisions still need handling, and same-URL-to-same-code is a privacy leak that also removes the ability to expire or re-issue a code independently of the URL.
- Auto-increment to base62 — rejected. Sequential codes are enumerable (a competitor can scrape every link) and a single DB counter is a central bottleneck that breaks with multiple writers. The distributed, non-sequential variant lives in [ADR 0007](0007-distributed-id-generation.md).
- Pre-generated key pool — rejected. The right answer at massive scale, but it adds a whole background service and table for no benefit at single-instance scale.

## Why base62, not base64

base64's alphabet includes `+`, `/`, and `=` padding, all unsafe or ambiguous in a URL path (`/` is a path separator; `+` can decode to a space). base62 drops those three, leaving only `[0-9a-zA-Z]`, which is unreservedly URL-safe and needs no escaping. The cost is marginally longer codes (a smaller base encodes fewer bits per char), negligible at 7 characters.

## Consequences

**Good**

- Unpredictable codes. `crypto/rand` means codes can't be guessed or enumerated, a privacy and security property the sequential scheme lacks.
- No shared state. The generator needs no counter and no coordination, so it is already correct across multiple instances. The distributed scheme changes it to avoid the per-insert DB check, not for correctness.
- Correctness is the database's job. The `UNIQUE` constraint is the single source of truth for uniqueness, immune to application races; the retry is a convenience around it.
- Swappable. The `IDGenerator` interface is the seam that lets a distributed generator drop in without touching `shortener` or `http`.

**Bad / trade-offs**

- A DB round-trip per create to confirm uniqueness. Creates are rare versus reads (~100:1), so this is not on the hot path.
- Theoretical retry exhaustion. At a 3.5T keyspace collisions are astronomically rare. Retries cap at 5 and return an error rather than loop forever; hitting the cap signals a bug, not bad luck.
- No same-URL dedup. Two users shortening the same URL get different codes (intentional, for privacy), so the table stores duplicate long URLs.
