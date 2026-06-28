# Data durability

`shortn` keeps all of its durable state in a single PostgreSQL 16 instance: one
StatefulSet with a 1Gi PVC in Kubernetes, one container plus a named volume in compose.
That database holds two tables — `links` (the `code → long_url` mapping plus
`created_at`, `expires_at`, `click_count`) and `click_events` (one row per click, the
source of truth for analytics). Everything else in the system is derived or transient:
Redis is a cache and a rate-limiter that re-warms itself, Redpanda buffers click events
in flight. If Postgres survives, the system survives. If Postgres loses data, nothing
else has it.

This note explains the durability design — what protects that data, what does not, and
which protections are deliberately deferred at this scale. The operational steps
(`make db-backup`, the proven-restore drill, the Kubernetes backup CronJob) live in the
[runbook](../runbook.md); this is the reasoning behind them.

## Durability is not availability

"Make Postgres production-ready" hides two separate problems that are easy to conflate:

- **Durability** — if data is lost or corrupted, can it be recovered? The threats are
  a bad deploy, a `DROP TABLE`, disk failure, ransomware. The answer is **backups**,
  optionally with point-in-time recovery.
- **Availability** — if the instance itself dies (node crash, OOM, restart, AZ outage),
  does the app keep serving? The answer is **replication and failover**: a standby ready
  to promote.

These are different failures with different fixes, and the fixes do not substitute for
each other. A replica is not a backup: streaming replication faithfully copies a
`DROP TABLE` to the standby in milliseconds, so the bad write lands on both copies. A
backup is not availability: restoring is minutes of downtime, not a hot takeover. A
system that wants both needs both, sized to its actual risk.

For `shortn`, durability is the genuine must before anything runs on the public internet.
Availability is softened by the architecture — see below — which is why the live deploy
runs a single Postgres and treats HA as deferred.

## The single Postgres is a deliberate single point of failure

There is exactly one Postgres, and that is a choice, not an oversight. The honest
framing: at this scale, a single managed-or-backed-up database is right-sized, and the
production answer to "what if the instance dies" is a managed or replicated database, not
a hand-built failover ensemble.

Two facts make the single instance defensible here:

1. **The redirect hot path is cache-first.** `GET /{code}` is served from Redis; Postgres
   is touched only on a cache miss or a write. So when Postgres is unreachable, cache
   hits still redirect (`302`) — only cold reads and creates fail. The
   [failure-mode table](../runbook.md#failure-mode-table) records exactly this: a
   `sony/gobreaker` circuit breaker opens after a handful of failures and the affected
   reads/creates fail fast with `503`, while `/readyz` flips so the load balancer pulls
   the instance. The blast radius of a Postgres outage is the write path plus cold reads,
   not the whole service.

2. **Recovery is automatic.** The breaker half-opens after a few seconds and recovers on
   its own; no app restart is needed once Postgres is back. The operator's job is to
   restart or fail over Postgres, not to babysit the API.

What this does *not* buy is zero-downtime survival of a Postgres node death — for the
duration of an outage, creates and cold reads are down. Accepting that window is the
tradeoff. The upgrade path is replication (below) or a managed database that ships HA in
the box.

## Backups: a backup you have never restored is not a backup

`shortn` uses **logical backups** — `pg_dump` in the compressed custom format (`-Fc`),
which produces a portable file restorable with `pg_restore`. Logical dumps are simple,
version-portable, and well-suited to a small database. Their cost is that they are
snapshots: everything written after the dump is lost if you have to restore, and large
databases restore slowly. Neither cost bites at this size.

The discipline that makes a backup real is the **restore drill**. A dump file sitting on
a disk is a hope, not a backup; the deliverable is a proven restore. The repo enforces
this with `make db-verify`, which loads the latest dump into a throwaway database and
prints live-vs-restored row counts — matching counts mean the backup actually round-trips.
In Kubernetes the daily `shortn-postgres-backup` CronJob (03:00, 7-day retention) writes
dumps to a dedicated PVC, and the runbook documents restoring from one. The commands for
all of this are in the [runbook](../runbook.md#backups--restore).

Two honest limits, both deliberate:

- **The dumps are not off-box.** Compose dumps land in a gitignored `backups/`; the k8s
  dumps sit on a PVC *in the same cluster*. They survive pod and node restarts but not a
  cluster loss. The 3-2-1 rule (three copies, two media types, one off-site) is the
  target; on free tier this satisfies neither the off-site nor the second-media leg.
  Production ships dumps off-cluster to object storage (S3/GCS) with lifecycle retention.
- **The recovery point is "since the last dump."** A logical snapshot can lose up to a
  day of writes (the CronJob interval). When that becomes unacceptable — when the recovery
  point objective needs to be seconds rather than a day — the upgrade is physical backups
  plus WAL archiving: `pg_basebackup` for a base image, then continuous archiving of the
  write-ahead log so a restore can replay to any chosen instant ("5 seconds before the bad
  migration"). That is more moving parts (an archive location, a `restore_command`,
  retention policy) and is not justified by the current data or its value, so it is
  deferred.

## Replication and partitioning, and why they are deferred

**Streaming replication** would have the primary stream its WAL — the ordered record of
every change — to one or more standbys that replay it continuously and stay seconds or
less behind. A standby can serve read-only queries and can be promoted to primary if the
primary dies. The pieces worth knowing:

- **Async vs sync.** Async (the usual default) lets the primary commit without waiting for
  the standby, so lag is tiny but a failover can lose the last few transactions. Sync makes
  the primary wait for the standby to confirm — zero loss, but every write pays a
  round-trip.
- **Replication slots** make the primary retain the WAL a standby still needs, so a
  briefly-offline standby can catch up instead of falling off a cliff.
- **Read/write split** is an application concern, not a database one: writes (`Create`) go
  to the primary, reads (`GetByCode`, `GetStats`) to the replica, usually via two
  connection pools. In this codebase that would mean `internal/store` growing a read pool
  alongside its write pool.
- **Failover** is either manual (`pg_promote()` plus repointing the app) or automated via
  a controller like Patroni, repmgr, or Stolon that watches health and promotes
  automatically. Automated failover is a heavyweight ensemble; at this scale a standby plus
  a documented manual-promote procedure is the right-sized HA story.

Replication is deferred here for a specific reason: the read load is already shielded.
Because redirects are served from Redis, a read replica's main payoff — offloading reads
from the primary — is modest, since the primary barely sees read traffic. Replication
remains a legitimate HA exercise (a standby to promote when the primary dies), but it is
not load-bearing for performance on this workload, and saying so honestly is part of the
design.

**Hash-partitioning** the tables would shard rows across multiple partitions to spread
write load and keep indexes small. With two tables this size, it adds operational
complexity and query-planning surprises for no measurable gain. It is deferred until table
size or write throughput actually demands it.

## The managed-vs-self-managed fork

There is a production-real path that does the opposite of building any of this by hand:
point at a managed Postgres (Neon, Supabase, RDS) where backups, point-in-time recovery,
and HA come built in. For most teams that is the genuine production choice, and the
free-tier options are viable for a live deploy. Self-managing backups and replication here
is a deliberate trade in favor of owning and understanding the internals; choosing managed
for a real deployment would be equally defensible. The two are not mutually exclusive —
the mechanism is understood here, and a managed database can carry it in production.

## See also

- [docs/runbook.md](../runbook.md) — the backup, restore, and verify procedures, the
  Kubernetes CronJob, and the Postgres failure-mode response.
- [deploy/k8s/shortn/templates/postgres-statefulset.yaml](../../deploy/k8s/shortn/templates/postgres-statefulset.yaml)
  and [postgres-backup-cronjob.yaml](../../deploy/k8s/shortn/templates/postgres-backup-cronjob.yaml).
- [internal/store/](../../internal/store/) — `postgres.go` (`links`), `analytics.go`
  (`click_events` / stats); where a read/write split would live.
</content>
</invoke>
