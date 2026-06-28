# Distributed IDs

How `shortn` mints a short, unique, non-sequential code for every link without
any of its API instances coordinating with each other. The mechanism is a
Snowflake-style 64-bit integer obfuscated into base62 by sqids. The decisions
behind it live in the ADRs; this document explains how the running system works.

Related decision records:

- [ADR 0004](../architecture/0004-id-generation-strategy.md) — the initial
  random-base62-with-collision-retry scheme.
- [ADR 0007](../architecture/0007-distributed-id-generation.md) — the move to
  Snowflake + sqids, and why the alternatives were rejected.
- [ADR 0014](../architecture/0014-worker-id-assignment.md) — leasing worker IDs
  from Redis under Kubernetes.

## Why the service is stateless

The API holds zero per-request memory between requests. Config arrives from
environment variables, link records live in Postgres, the cache lives in Redis,
and there are no global mutable variables holding request data. Graceful
shutdown drains in-flight requests and exits; nothing is lost because nothing
was held.

That property is what makes horizontal scaling possible. Several identical
copies of the API run behind a load balancer (nginx under Compose, a Kubernetes
Service in the cluster), and any copy can serve any request because they all
read and write the same Postgres and the same Redis and keep nothing private in
RAM. Adding instances adds capacity; routing a request to one instance versus
another never changes the answer.

The one piece of per-instance identity that matters is the worker ID, and the
system goes out of its way to keep that from being process-local state — see
[Worker ID assignment](#worker-id-assignment).

## The coordination problem

A single process can mint unique codes with an in-memory counter: increment,
return, done. Run two copies and each has its own counter starting at zero, so
both hand out `1`, `2`, `3`, and those codes collide in the database. Two
different URLs end up wanting the same short code; the unique constraint on
`links.code` rejects the second write. There is no shared counter variable
across processes — each process has its own address space, and across machines
there is no shared memory at all.

The early design (ADR 0004) sidestepped this with random base62 plus
collision-retry: generate seven random characters from `crypto/rand`, insert,
and on a `23505` unique-violation regenerate and retry (capped at five
attempts). That scheme is stateless and needs no coordination, but it pays a DB
round-trip to confirm uniqueness and offers no time ordering. Snowflake replaces
it by swapping the `IDGenerator` implementation only — the domain and HTTP
layers never changed.

## Snowflake: identity baked into the number

The insight behind Snowflake (Twitter, 2010) is that if enough identity
information is packed directly into the ID, uniqueness needs no shared counter.
`shortn` builds a 64-bit integer from three fields:

```text
[ timestamp: 41 bits ][ worker ID: 10 bits ][ sequence: 12 bits ]
```

| Field     | Bits | Range      | Meaning |
| --------- | ---- | ---------- | ------- |
| Sign      | 1    | always 0   | Keeps the value positive in signed 64-bit |
| Timestamp | 41   | 0 – 2⁴¹ ms | Milliseconds since a fixed project epoch (~69 years of range) |
| Worker ID | 10   | 0 – 1023   | Which instance generated this ID |
| Sequence  | 12   | 0 – 4095   | Disambiguates IDs minted in the same millisecond |

The packing is three shifts and two ORs, in `internal/idgen/snowflake.go`:

```go
id := (uint64(now) << (workerBits + sequenceBits)) |
      (uint64(g.workerID) << sequenceBits) |
      uint64(g.sequence)
```

The epoch is a project constant, `954028800000` Unix milliseconds (2000-03-26
UTC). Subtracting it before packing keeps the timestamp field small, which keeps
the encoded code short. The epoch must never change once codes are in
production: it is baked into every issued ID.

Uniqueness holds in every case. Same worker and same millisecond differ by
sequence. Same worker, different milliseconds differ by timestamp. Different
workers differ by the worker field regardless of the other two. No network call
is involved; `Generate()` reads the clock and does arithmetic.

A single worker can emit 4096 IDs per millisecond — about four million per
second. If a worker exhausts all 4096 sequence slots within one millisecond, the
generator sleeps one millisecond and resumes with sequence reset to zero, so it
waits for the clock rather than reusing a slot.

### Concurrency

`lastMs` and `sequence` are shared mutable state read and written by every
`Generate()` call, and many goroutines call it at once. A `sync.Mutex` serialises
the whole packing path so at most one goroutine reads-then-writes those fields at
a time; without it two goroutines could land on the same sequence and emit the
same ID. The mutex is the first field of `SnowflakeGenerator`, by Go convention,
signalling that the struct is concurrency-sensitive.

### The backward-clock guard

Snowflake's correctness depends on the timestamp never going backward. The
system clock can regress: NTP steps it back after correcting drift, a VM resumes
from a snapshot, a leap second lands. If the clock moves back, a millisecond the
generator already used becomes "current" again, and an ID minted at that
timestamp could collide with one already in the database.

`Generate()` refuses rather than risk that:

```go
if now < g.lastMs {
    return "", ErrClockRegressed
}
```

`ErrClockRegressed` propagates to the caller, which can surface a `503`. The
failure is loud and the bad ID is never written. The alternative — sleeping
until the clock catches up — is fine for a 1–2 ms hiccup but unacceptable for a
100 ms+ NTP step correction applied to every create. Failing fast keeps the
latency bounded and turns a clock problem into an operator-visible signal.

## sqids: hiding the sequence

A raw Snowflake integer encodes time, so consecutive creates produce nearly
consecutive integers. Base62-encoding those directly would yield codes that
differ by one character, and anyone holding `0aBcD2` could walk to `0aBcD1` and
`0aBcD3` and scrape every link on the service. That is an enumeration attack: it
exposes links the owner treats as unlisted and leaks how much traffic was
created in a given window.

The fix keeps the integer's uniqueness and only changes its public
representation. `shortn` runs the 64-bit ID through
[sqids](https://github.com/sqids/sqids-go), configured with a shuffled base62
alphabet from `SQIDS_ALPHABET`. sqids turns the integer into a short, URL-safe,
non-sequential string, and the transform is deterministic and reversible given
the alphabet, so consecutive integers produce visually unrelated codes:

```text
1750123456789012345 → "kZ7mQ2pX"
1750123456789012346 → "wR9nL4sY"
```

sqids obfuscates; it is not encryption. The alphabet is a permutation, not a
secret key, and anyone with enough sample codes could in principle recover the
ordering. It defeats casual enumeration, which is the actual threat for an
anonymous link shortener, and that is all it is asked to do.

The alphabet must never change after codes are issued. A different alphabet
decodes an existing code to a different integer, pointing it at the wrong link or
nothing at all. Like the epoch, treat `SQIDS_ALPHABET` as permanent in
production. The default in `internal/config` is a fixed shuffled base62 string;
a deployment may override it, but only before its first code is minted.

## Worker ID assignment

The whole uniqueness argument rests on no two live instances sharing a worker
ID. Two instances both holding worker ID 5, minting an ID in the same
millisecond with the same sequence, produce the byte-identical integer and the
same code. The DB unique constraint catches it as a failed-and-retried create
rather than silent corruption, but it makes scaling past one writer unsafe
unless worker IDs are guaranteed distinct.

`shortn` resolves the worker ID two ways, chosen at startup in
`cmd/api/main.go`:

**Explicit `WORKER_ID` (Compose).** Each service in the Compose file declares its
own value by hand — `api-1` is `0`, `api-2` is `1`, `api-3` is `2`. The startup
code parses it and refuses to start if it is missing or outside `[0, 1023]`. This
is simple and needs no extra infrastructure, but it is hand-assigned: copy a
service block and forget to change the number, and two instances silently share
an ID until they collide under load.

**Redis lease (Kubernetes).** In the cluster every API pod runs the same image
with the same ConfigMap, so a single hand-assigned `WORKER_ID` would give every
pod `0`. When `WORKER_ID` is empty, the pod instead leases a slot. The mechanism
lives in `internal/idgen/lease.go`:

- `AcquireWorkerID` walks slots `0..1023` and `SETNX`s `shortn:worker:<n>` to the
  pod's identity (its `INSTANCE_ID`) with a 30-second TTL. The first slot it wins
  is its worker ID. If every slot is held, it errors and the pod refuses to
  start — ID assignment cannot fail open the way the cache can.
- A background heartbeat renews the TTL every 10 seconds (about one-third of the
  TTL, so two missed renewals are tolerated) using a Lua script that extends the
  key only if the pod still owns it.
- On graceful shutdown `Release` deletes the slot, again only if still owned, so
  a replacement pod reuses the ID immediately instead of waiting out the TTL.
- If a renewal ever finds the slot taken over (the pod stalled long enough for
  the TTL to lapse), it logs at error level: the pod may now be sharing a worker
  ID and should be restarted.

This is the coordination-lease pattern — Twitter's original Snowflake leased
worker IDs from ZooKeeper; at this scale Redis is the right size. The full
rationale, including why StatefulSet ordinals and a key-generation service were
rejected, is in [ADR 0014](../architecture/0014-worker-id-assignment.md).
