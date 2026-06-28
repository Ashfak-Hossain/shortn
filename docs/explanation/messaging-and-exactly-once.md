# Messaging and exactly-once analytics

How `shortn` records clicks without slowing down the redirect, and what its "exactly-once"
guarantee actually means. The decision and the alternatives that were weighed live in
[ADR 0008](../architecture/0008-messaging-and-delivery-semantics.md); this document explains
the mechanism behind that decision and where the guarantee stops.

## The shape of the system

A URL shortener is read-heavy and write-light. Creating a link is rare; resolving one is the
hot path. One viral link is a single Postgres row that absorbs millions of redirects, a
read-to-write ratio that is easily 1000:1 or higher. The cache honors that asymmetry by serving
redirects cache-aside from Redis, so the hot read frequently never touches Postgres.

Recording a click is a write, and it has nothing to do with serving the redirect. If the
redirect handler ran `UPDATE links SET click_count = click_count + 1` inline, every redirect —
including the ones Redis just served for free — would block on a durable Postgres write. Worse,
a viral link is one row, so concurrent clicks serialize on that row's lock; throughput caps near
`1 / (lock-hold time)`, and the queued updates hold pgx pool connections while they wait,
draining the bounded pool until unrelated redirects and creates also block in `pool.Acquire`. A
fire-and-forget counter would take the whole service down.

So `shortn` moves the click off the redirect path entirely. The redirect does its read and
returns the 302; the click becomes an event, published after the response is on the wire and
treated as non-fatal; a separate `cmd/analytics` consumer drains those events into Postgres on
its own schedule.

```text
client            cmd/api              Redpanda            cmd/analytics        Postgres
  │  GET /aB3xZ      │              (topic shortn.clicks)       │                  │
  ├─────────────────>│ resolve (cache/db)    │                  │                  │
  │  302 Location    │                       │                  │                  │
  │<─────────────────┤ publish LinkClicked   │                  │                  │
  │  (already gone)  ├──────────────────────>│   poll/fetch     │                  │
  │                  │                       │<─────────────────┤                  │
  │                  │                       ├─────────────────>│ INSERT click +   │
  │                  │                       │                  │ offset (1 txn)   ├──>│
```

`cmd/api` and `cmd/analytics` never speak to each other. Neither holds a connection or a
hostname for the other; both know only the topic `shortn.clicks` on Redpanda. That indirection
buys independent scaling (the read tier and the write tier size separately), independent failure
(an analytics outage is "lag," not "links stop working"), and independent deployment. The cost
is a distributed system: a broker to operate, eventual consistency on the counts, and a pipeline
that must survive duplicates and restarts without inflating those counts.

## The log model

Redpanda is Kafka-API-compatible, so the data model is Kafka's: a topic is a named,
append-only commit log, split into N partitions, each an independent ordered log on disk. A
record, once written, is immutable; consumers only append at the tail and read from anywhere.

An **offset** is a monotonically increasing integer identifying a record's position within one
partition. Partition 0's offset 5 and partition 1's offset 5 are unrelated. A consumer's
progress is a committed offset per partition — "give me everything from offset 42 onward." That
the bookmark is just an integer is the hinge of the whole design: an integer can be written into
Postgres in the same transaction as the data it accounts for.

Ordering is guaranteed only within a partition, never across the topic. The partition a record
lands in is `hash(key) mod numPartitions`. `shortn` keys the `LinkClicked` event by the short
code (`internal/events/events.go`, `rec := &kgo.Record{Key: []byte(e.Code), ...}`), so every
click on a given code lands on the same partition and is totally ordered relative to itself.
Short codes are high-cardinality and near-uniform (Snowflake + sqids), so hashing them spreads
load evenly across partitions rather than creating a hot one — except, honestly, for a single
viral link, which is still one partition's worth of traffic by construction.

The log retains records for a configured window independent of whether anyone consumed them, so
a consumer can be down at produce time and resume later, and a new consumer group can replay
history from offset 0. Replay reaches back only as far as retention keeps the data; the broker
is a durable buffer for a window, not an infinite archive.

## The producer is best-effort, on purpose

The publish runs in `internal/http/links.go`, after `http.Redirect` has written the 302:

```go
http.Redirect(w, r, link.LongURL, http.StatusFound)
// ...build the LinkClicked event...
pubCtx := context.WithoutCancel(r.Context())
go func() {
    if err := h.publisher.Publish(pubCtx, event); err != nil {
        h.logger.Error("publish click event failed", "err", err, "code", code)
    }
}()
```

Three deliberate choices live in those lines. The publish happens *after* the redirect, so
analytics adds no latency to the user's request. It runs in a goroutine, so the handler returns
immediately. And its context is `context.WithoutCancel(r.Context())` — detached from the
request's cancellation (which fires the instant the handler returns) but keeping the request's
trace context, so the publish span joins this redirect's trace instead of orphaning into a new
one. A publish failure is logged and dropped.

The consequence has to be stated plainly: a dropped publish is a click that never entered the
log and is **never counted**. This is at-most-once on the produce side, and it is intentional —
redirect correctness must never depend on analytics. The producer is the franz-go client with
default settings (`NewKafkaPublisher`), which enables the idempotent producer by default, so a
network blip that causes franz-go to resend a record it already wrote is deduplicated by the
broker. That handles producer *retries*; it does nothing for a publish the API chose to drop.

## The guarantee boundary

"Exactly-once" is a misleading phrase, so here is the precise version. The pipeline has two
segments with two different guarantees:

1. **Produce side — at-most-once.** Covered above: a failed publish is logged and dropped, so
   the click is lost. The system does not claim every click reaches the log.
2. **Broker → Postgres — effectively-once.** Once a record is committed to the log, it is
   reflected in `click_events` exactly once across crashes, restarts, and rebalances.

The one-sentence claim is: at-most-once produce, layered under effectively-once broker→Postgres
processing. Everything that follows is segment 2.

Segment 2 is hard because every network call has three outcomes, not two: success, failure, and
*unknown* — the broker acted but the ack was lost. From the sender's side "I sent it and the ack
timed out" is indistinguishable from "it never landed," which is the Two Generals Problem.
Retrying on unknown gives at-least-once (no loss, but duplicates); not retrying gives
at-most-once (no duplicates, but loss). Exactly-once *delivery* over a lossy channel is
impossible; exactly-once *processing* is achievable by making duplicate deliveries produce no
duplicate effect.

The naive consumer double-counts. It has two writes — process the event into Postgres, then
commit the offset to Kafka — and they are not atomic. A crash between them either replays the
record (process-then-commit, inflating counts) or loses it (commit-then-process). Over millions
of events and routine restarts, that gap is hit with certainty.

`shortn` closes the gap by storing the offset in the **same Postgres transaction** as the click.
`process` in `cmd/analytics/main.go`:

```go
tx, err := pool.Begin(ctx)
// ...
if err := st.InsertClick(ctx, tx, e); err != nil { return err }
if err := st.SaveOffset(ctx, tx, group, rec.Topic, rec.Partition, rec.Offset+1); err != nil { return err }
return tx.Commit(ctx)
```

`InsertClick` writes the row with `ON CONFLICT (event_id) DO NOTHING`; `SaveOffset` upserts
`rec.Offset+1` (the *next* offset to read) into `kafka_offsets`, keyed by
`(consumer_group, topic, partition)`. Both statements commit or roll back together, so "recorded
the click" and "advanced past it" are a single atomic fact. There is no processed-but-not-
recorded gap to lose, and no recorded-but-not-advanced gap to replay.

The other half is where the consumer resumes. franz-go is built with `DisableAutoCommit()` — it
never commits to Kafka's `__consumer_offsets`. On partition assignment, the `OnPartitionsAssigned`
hook loads each partition's last offset from Postgres via `LoadOffset` and calls
`cl.SetOffsets(...)` so the fetch resumes from the offset the database remembers, not the one
Kafka would otherwise hand back. A partition this group has never committed falls through to
`ConsumeResetOffset(AtStart)`. The startup path and the rebalance-assign path run the *same*
logic; a rebalance mid-run is just startup for a subset of partitions.

```text
restart / rebalance assigns P2:
   Postgres kafka_offsets says offset = 4915
        │
        ▼
   SetOffsets(P2 → 4915)  ──► fetch resumes at 4915, never re-reads ≤ 4914
```

This makes Postgres the single source of truth for "how far have we consumed," and it moved
atomically with the inserts. The consumer still *joins* the group — it wants Kafka's partition
assignment and rebalancing — it just ignores Kafka's offsets for progress. Accurate one-liner:
uses consumer groups for assignment, ignores Kafka offsets for progress.

The `event_id` primary key with `ON CONFLICT DO NOTHING` is defense-in-depth on top of the
transactional offset, not the primary mechanism. If anything ever does force a replay — a bug, a
manual offset reset, a torn batch — the duplicate insert is silently absorbed rather than
counted twice. The error path leans on this directly: when `process` fails, the consumer logs
and calls `os.Exit(1)` rather than advancing past the record; on restart it seeks from Postgres
and reprocesses that exact record, and `ON CONFLICT` makes any partial replay harmless.

This is the offset-in-transaction pattern, not Kafka's transactional EOS feature. Kafka's EOS
coordinates `__consumer_offsets` with a Kafka transaction; that only helps when the sink is also
Kafka. Here the sink is Postgres, so the offset is routed to Postgres, which is the only way to
make "data written" and "offset advanced" one atomic decision against that sink.

## Operational notes

A queue is a shock absorber, not a fix for sustained overload. A click burst becomes queue depth
(consumer lag) that the consumer pays down at whatever rate Postgres sustains; the redirect path
never feels it. But if the *sustained* arrival rate exceeds what the consumer plus Postgres can
drain, lag grows until it hits retention and the oldest events are dropped. A buffer converts a
spike into latency; it does not rescue a throughput-underwater system. The fix for that is more
partitions and consumers or a faster sink.

Partition count is the parallelism ceiling for the life of the topic. Within a group each
partition has exactly one owner, so consumers beyond the partition count sit idle as warm
standbys. Growing the partition count later changes `hash(key) mod numPartitions`, moving keys
to new partitions and breaking per-code ordering across the resize, so pick a count with
headroom up front.

Two liveness clocks govern a consumer. Heartbeats run on a background thread, so a consumer can
keep heartbeating while its processing loop is wedged on a slow Postgres transaction;
`max.poll.interval.ms` is the clock that catches that case and evicts the member, triggering a
rebalance and a reprocess of the uncommitted batch. The defense is bounded per-batch work — small
batches, fast transactions — rather than only raising the timeout.

Asynchrony breaks the stack trace. In a synchronous call a failure is one connected trace from
cause to symptom; here the producer's job ends at publish, so an uncounted click could be a
dropped publish, a lagging consumer, a crash-looping poison message, or a rolled-back
transaction. The pipeline is instrumented for exactly this: the W3C `traceparent` is injected
into the record headers (never the JSON payload) on publish and extracted on fetch, so the
consumer's per-record span is a child of the API's publish span and both services stitch into
one end-to-end trace, alongside `analytics.clicks.processed` throughput and `analytics.consumer.lag`
gauges. Correlate on `event_id`, the trace, and lag — there is no single thread to follow.

## Where this stops

A malformed event is a poison pill: `process` returns the unmarshal error, the consumer exits,
and on restart it hits the same record again. Today that is fatal by design; a dead-letter topic
is the production answer and is not yet built. The producer drop window (at-most-once produce) is
also a real, accepted source of undercount — the system trades a small, silent loss of clicks for
a redirect path that never waits on or fails because of analytics.
