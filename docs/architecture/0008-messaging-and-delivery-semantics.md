# Use Redpanda (Kafka API) with exactly-once processing for click analytics (0008)

**Status:** Accepted, 2026-06-14

## Context

An earlier design tracked clicks with a synchronous `UPDATE click_count` inside the redirect
handler. That welds the system's most latency-sensitive, highest-volume operation — the
redirect, a read — to a database write. A URL shortener is ~100:1 reads:writes, so under a
viral link thousands of concurrent redirects contend on one row and drain the connection
pool, and redirect latency tracks DB write latency.

Click-tracking runs off the redirect hot path. The API publishes a `LinkClicked` event; a
separate `cmd/analytics` consumer drains those events into Postgres on its own schedule.
Producer and consumer fail and scale independently: the consumer can be down without
dropping clicks that already reached the log, and redirects never wait on analytics.

The hard sub-problem is delivery guarantees. Every network call has three outcomes —
success, failure, and _unknown_ (a lost ack) — so any retry risks a duplicate. The default
any real broker gives you is at-least-once, which means consumers see duplicates and will
inflate click counts unless they are made idempotent (or exactly-once is engineered
end-to-end). The system targets exactly-once directly so the guarantee against an external
sink is understood precisely, including where it stops.

Two constraints shape the choice:

1. **Self-hosted, low footprint.** Managed free Kafka tiers have largely disappeared, so the
   broker is self-hosted in Docker locally and on the deployment VM. Resource footprint
   matters, especially once the observability stack (Prometheus/Grafana/Loki/Tempo) shares
   that machine.
2. **Redirect correctness must never depend on analytics.** Analytics is best-effort;
   redirects are not. This is non-negotiable and decides the producer design.

Four brokers and two ways to get exactly-once were evaluated:

1. **NATS JetStream** — lightweight, at-least-once; exactly-once still needs app-level
   dedupe.
2. **Apache Kafka (KRaft)** — the resume-standard log; JVM-based, heavier to operate.
3. **Redpanda** — single C++ binary that speaks the Kafka wire protocol; no JVM, no
   ZooKeeper; much smaller footprint.
4. **RabbitMQ** — classic smart-broker/delete-on-consume queue; weaker replay/log
   semantics.

And for the exactly-once mechanism: **Kafka transactions** (atomic only within Kafka) vs.
the **offset-in-transaction** pattern (offset stored in the sink's own transaction).

## Decision

The broker is **Redpanda** (Kafka-API-compatible), accessed with **`franz-go`**
(`github.com/twmb/franz-go`). The wire protocol, client, and every concept (partitions,
offsets, consumer groups, idempotent producers, transactions) are identical to Kafka.

- **Topic:** `shortn.clicks`, with the short `code` as the record key so all clicks for one
  link land in the same partition and stay ordered.
- **Producer (API):** publish the `LinkClicked` event **after** `http.Redirect` returns,
  and treat publish failure as **non-fatal** — log it, never fail the redirect. The
  idempotent producer (enabled by default in franz-go: broker assigns a Producer ID, each
  record carries a per-partition sequence number) deduplicates the producer's _own_ retries
  on the broker side.
- **Consumer (`cmd/analytics`):** exactly-once _processing_ into Postgres via the
  **offset-in-transaction** pattern. Kafka auto-commit is disabled; for each record (or
  per-partition batch) a single Postgres transaction inserts the `click_events` row **and**
  upserts the Kafka offset into a `kafka_offsets` table, then commits — so "processed this
  record" and "advanced past it" are one atomic fact. On partition assignment the consumer
  seeks from the offset stored in Postgres, not from Kafka's `__consumer_offsets`, because
  Postgres owns the side effect and is therefore the source of truth for progress.
  `event_id` (a producer-assigned UUID) is the `click_events` primary key, inserted with
  `ON CONFLICT (event_id) DO NOTHING` as defense-in-depth.
- **Config:** `KafkaBrokers` (broker list), `KafkaTopic`, `KafkaGroup`.

**The guarantee, stated precisely:** at-most-once produce, layered under effectively-once
broker→Postgres processing. This is **not** end-to-end exactly-once. A click whose publish
never reaches the broker (e.g. broker down) is simply not counted — the conscious price of
keeping analytics off the redirect's critical path.

## Alternatives considered

- **NATS JetStream** — rejected. Lighter and genuinely good, but it is at-least-once;
  exactly-once would still be application-level dedupe, and it exercises fewer of the
  industry-standard concepts (partitions, consumer groups, the log abstraction).

- **Apache Kafka (KRaft mode)** — rejected on operational weight, **not** capability. Same
  API, same concepts, same `franz-go` code — but the JVM footprint is heavier on the single
  shared box, which matters once the observability stack lands. Redpanda gives the identical
  Kafka surface at a fraction of the memory. If the project ever needed features Redpanda
  lacked, switching back is a config change, not a code change.

- **RabbitMQ** — rejected. A smart-broker, delete-on-consume queue with per-message acks is
  a different (older) model. The append-only log abstraction — retained, replayable,
  offset-addressed — is what makes replay and the offset-in-transaction pattern possible.

- **Kafka transactions alone (read-process-write) for exactly-once** — rejected as the
  mechanism for our sink. Kafka transactions are atomic **only within Kafka** (topic appends
  + offset commits to `__consumer_offsets`); the transaction coordinator has no hook into a
  Postgres transaction, so it cannot make a Kafka→Postgres write atomic. That limitation is
  precisely why the offset-in-transaction pattern exists.

- **At-least-once + `event_id` `ON CONFLICT DO NOTHING` as the primary mechanism** —
  rejected as the _primary_ design but **kept as defense-in-depth**. It is correct and
  simpler, but it does not capture where Kafka's exactly-once stops; the
  offset-in-transaction pattern does. The unique key remains the safety net for bugs, manual
  replays, and future refactors.

- **Transactional outbox on the produce side** (to reach true end-to-end exactly-once) —
  rejected. Writing the event to an outbox table _inside the link-resolution transaction_
  would make the publish exactly-once, but it would put analytics back on the redirect's
  database transaction, i.e. back on the hot path. The system accepts at-most-once produce
  instead. This is the central trade.

## Consequences

**Good:**

- Redirect latency is decoupled from analytics and from DB write speed; a viral burst is
  absorbed by the log on disk, not by the redirect path.
- **Exactly-once _processing_ into Postgres:** no double-counts across consumer crashes,
  restarts, or consumer-group rebalances — proven by killing the consumer mid-transaction
  and asserting exact counts.
- Replay-safe (the log is retained, not delete-on-consume) and a clean consumer-group
  scaling story.
- Real Kafka fluency on a lean engine: `franz-go` + Redpanda use far less memory than JVM
  Kafka on the shared box.
- A defensible guarantee boundary: exactly-once starts at the committed record and stops at
  the best-effort publish.

**Trade-offs / costs:**

- **Not end-to-end exactly-once.** A publish that never reaches the broker is an uncounted
  click (at-most-once produce). Accepted by design: analytics is best-effort, redirects are
  not.
- **Offset management lives in our code.** Disabling auto-commit means we own the rebalance
  lifecycle (`OnPartitionsRevoked`/`OnPartitionsAssigned`), the seek-from-Postgres logic,
  and a `kafka_offsets` table. This is a meaningfully more complex consumer than the
  auto-commit default — and the part most likely to harbour a subtle bug, so it gets the
  crash/restart test.
- **Partition count is a near one-way door.** Increasing partitions later rehashes the
  `code`→partition mapping and breaks per-link ordering for existing keys, so the count must
  be chosen deliberately up front.
- **Licensing.** Redpanda's Community Edition is source-available (Business Source License),
  not Apache-2.0 like Kafka — irrelevant for this self-hosted project, but worth knowing for
  any commercial use. Verify current terms before relying on them.
- **More moving parts:** a second binary (`cmd/analytics`), a new dependency (`franz-go`),
  and an extra service (plus the Redpanda dual-listener config gotcha) in compose and
  Kubernetes.

> Deep dive:
> [docs/explanation/messaging-and-exactly-once.md](../explanation/messaging-and-exactly-once.md)
> (the mechanism and the guarantee boundary).
