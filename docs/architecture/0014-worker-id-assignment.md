# Worker ID assignment via Redis lease (0014)

**Status:** Accepted, 2026-06-24

## Context

[ADR 0007](0007-distributed-id-generation.md) gives `shortn` coordination-free Snowflake IDs:
`[timestamp | worker_id (10 bits) | sequence (12 bits)]`. The `worker_id` is the field that keeps
two instances from minting the same ID. Each instance has its own `sequence` counter, so two
instances sharing a worker ID can produce the byte-identical ID (same millisecond, same
sequence) and therefore the same short code. 0007 assigned the worker ID from a `WORKER_ID` env var
(0–1023) and flagged that "two instances with the same `WORKER_ID` silently share an ID" as an
operational risk to be managed by hand.

That hand-assignment works in docker-compose (`api-1/2/3` get `0/1/2` explicitly). It breaks in
Kubernetes ([ADR 0011](0011-orchestration.md)): the API is a Deployment whose pods all run the
same image with the same ConfigMap, so every pod gets `WORKER_ID=0`. Concurrent creates across pods
then collide, caught by the DB unique constraint but as failed/retried creates, not correctness.
For that reason the HPA / `maxReplicas > 1` is unsafe with hand-assignment alone.

Horizontal create-scale must be safe: each instance has to acquire a unique worker ID with no
hand-assignment, working for a dynamically-scaled set of identical pods.

## Decision

1. **Each instance leases a worker-ID slot from Redis at startup.** It scans `n = 0..1023` doing
   `SET shortn:worker:<n> <owner> NX EX 30s`; the first success is its worker ID. `NX` makes the
   claim atomic: two pods racing for the same slot, only one wins. The `owner` token is
   `INSTANCE_ID` (defaults to the pod name, unique per instance) so ownership is provable.

2. **A heartbeat holds the lease; shutdown releases it.** A goroutine renews every 10 s via a Lua
   compare-and-`pexpire` (renew only if the slot is still owned); graceful shutdown does a Lua
   compare-and-`del` so the slot frees immediately for a replacement pod. TTL (30 s) is well above
   the heartbeat (10 s), so two missed renewals are tolerated before expiry.

3. **ID assignment fails hard.** Unlike the cache, which fails open ([ADR 0006](0006-caching-strategy.md)),
   a pod that cannot reach Redis at startup refuses to boot: a pod with no/duplicate worker ID
   is unsafe, so Kubernetes restarts it until Redis is reachable.

4. **An explicit `WORKER_ID` env still overrides the lease.** docker-compose keeps assigning IDs by
   hand; the lease is the default only when the env is unset (i.e. in Kubernetes). Implemented in
   [`internal/idgen/lease.go`](../../internal/idgen/lease.go), wired in `cmd/api/main.go`.

Scaling the API to 4 pods produces worker ids 0, 1, 2, 3 (`shortn:worker:0..3` in Redis), with no
collisions.

## Alternatives considered

- **StatefulSet ordinals** (derive the worker ID from a stable pod name `shortn-api-N`) — rejected:
  it uses a stateful primitive for a stateless service purely to get identity, and brings ordered
  (slower) rollouts/scaling. The API is stateless and stays a Deployment; the lease gives identity
  without that baggage.
- **ZooKeeper / etcd coordination service** (Twitter's original Snowflake approach) — rejected as
  over-engineering: a heavyweight ensemble (3–5 nodes for HA, leader election, real ops burden) to
  hand out a handful of integers. It would become the largest and most failure-prone component in
  the stack and reduce uptime. This is the path at roughly 100× scale or many dynamically-scheduled
  ID nodes, not at this size.
- **Key Generation Service (KGS)** (a service pre-mints codes; app servers draw batches) — rejected:
  it is the canonical large-scale shortener design and removes worker IDs entirely, but it is a
  whole new service plus datastore that replaces the Snowflake approach, too big a rebuild for the
  benefit at this scale.
- **Hashing the pod IP into the worker-ID space** — rejected: hash collisions reintroduce the exact
  duplicate-ID bug this ADR exists to remove.
- **Reuse the already-running Redis** — chosen: no new infrastructure, atomic primitives
  (`SET NX`, Lua CAS), TTL-based reclaim of dead pods' slots. It is the coordination-lease pattern
  (what ZooKeeper would do) at the right weight for this system.

## Consequences

**Easier:**

- Horizontal create-scale is collision-safe; the HPA can scale the API freely, and `maxReplicas > 1`
  is production-safe rather than a demo.
- No hand-assignment of worker IDs in Kubernetes; the 0007 operational-discipline footgun is gone.
- Dead pods' slots self-reclaim (TTL) and graceful exits free them immediately, so IDs are recycled.

**Harder / what this now owes:**

- API startup now depends on Redis. For the cache, Redis is non-critical (fail open); for ID
  assignment it is load-bearing (fail hard, by design). Redis is a single instance, so its
  availability matters more, and Redis HA is a likely future ADR.
- Residual risk, the standard lease caveat: a pod network-partitioned from Redis longer than the TTL
  while still serving could have its slot reclaimed, giving two pods the same worker ID and a
  possible collision (only if both mint in the same millisecond at the same sequence). Mitigated by
  the generous TTL plus heartbeat and an ERROR log on a lost lease; eliminating it would need fencing
  tokens, which Snowflake IDs do not carry.

This record supersedes the worker-ID assignment mechanism of [ADR 0007](0007-distributed-id-generation.md)
(the `WORKER_ID` env var remains a supported override but is no longer the primary path in
Kubernetes). The Snowflake ID structure and generator from 0007 are unchanged.
