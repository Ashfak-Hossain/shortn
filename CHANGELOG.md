# Changelog

The capabilities `shortn` gained over its development, newest first. Each entry links the
decision record behind it. Granular history is in the git log; this is the readable spine.

## Live public deployment and dashboard

- A React dashboard (Vite + TypeScript) for creating links and viewing per-code analytics,
  served under `/app` ([ADR 0015](docs/architecture/0015-web-dashboard-and-access.md)). See
  [deployment](docs/deployment.md).
- Deployed on the public internet on single-node k3s behind Cloudflare (edge TLS, DDoS, hidden
  origin). Short links own the root; the dashboard sits at `/app`.
- Locked down for public exposure: listing and deleting links are gated by an admin key
  (`X-Admin-Key`, fail-closed, constant-time compare); the rate limiter keys on the rightmost
  `X-Forwarded-For` entry; health, readiness, and metrics moved to an internal-only ops port.

## Production hardening

- Each instance leases a unique Snowflake worker id from Redis, making horizontal scale
  collision-safe and superseding the earlier static assignment ([ADR 0014](docs/architecture/0014-worker-id-assignment.md)).
- Create-time blocking of internal and metadata addresses (private, loopback, link-local), with
  a documented threat model ([SECURITY.md](SECURITY.md), [security explainer](docs/explanation/security-ssrf-and-redirects.md)).
- Postgres `pg_dump` backups with a proven restore, plus a Kubernetes backup CronJob
  ([data-durability explainer](docs/explanation/data-durability.md), [runbook](docs/runbook.md#backups--restore)).

## Load testing and performance

- A reproducible k6 report (throughput and p50/p95/p99), an autoscaling demonstration, and one
  bottleneck found, fixed, and re-measured: adding an nginx upstream keepalive pool cut redirect
  p99 roughly 15x. See [performance](docs/performance.md).

## Kubernetes, GitOps, and infrastructure as code

- Packaged as a Helm chart and delivered by ArgoCD with pull-based GitOps; the API autoscales
  via an HPA and deploys are zero-downtime rolling updates
  ([ADR 0011](docs/architecture/0011-orchestration.md), [ADR 0012](docs/architecture/0012-gitops-delivery.md)).
- The cluster, namespaces, and ArgoCD install are declared in Terraform
  ([ADR 0013](docs/architecture/0013-infrastructure-as-code.md)); secrets are committed only as
  encrypted SealedSecrets.

## Observability

- Metrics, logs, and traces through the OpenTelemetry SDK, exported directly to Prometheus,
  Loki, and Tempo with Grafana dashboards on the golden signals; one click is traceable end to
  end across the queue ([ADR 0010](docs/architecture/0010-observability.md),
  [observability explainer](docs/explanation/observability.md)).

## Resilience

- Per-call timeouts, a Redis-backed distributed rate limiter (`429` + `Retry-After`), a circuit
  breaker on Postgres, and idempotency keys, with each failure mode proven by a chaos script
  ([ADR 0009](docs/architecture/0009-resilience.md), [runbook](docs/runbook.md#failure-mode-table)).

## Asynchronous analytics

- Click recording moved off the redirect hot path onto Redpanda with an exactly-once consumer:
  the queue offset is committed in the same Postgres transaction as the click insert
  ([ADR 0008](docs/architecture/0008-messaging-and-delivery-semantics.md),
  [messaging explainer](docs/explanation/messaging-and-exactly-once.md)).

## Distributed IDs and horizontal scaling

- Short codes come from a coordination-free Snowflake generator obfuscated with sqids; the API
  runs stateless behind nginx across multiple instances
  ([ADR 0007](docs/architecture/0007-distributed-id-generation.md),
  [distributed-ids explainer](docs/explanation/distributed-ids.md)).

## Caching

- A Redis read-through cache on the redirect path with negative caching, singleflight, and
  fail-open behaviour, so a cache hit never touches Postgres
  ([ADR 0006](docs/architecture/0006-caching-strategy.md)).

## Core service

- Create and redirect over a chi HTTP layer, a pgx Postgres store, URL normalization, and
  random base62 ids behind the `IDGenerator` interface
  ([ADR 0004](docs/architecture/0004-id-generation-strategy.md),
  [ADR 0005](docs/architecture/0005-url-normalization.md)).
- Foundation: a Go service with 12-factor configuration, a distroless container image, and CI
  ([ADR 0001](docs/architecture/0001-language-and-runtime.md),
  [ADR 0002](docs/architecture/0002-config-from-environment.md),
  [ADR 0003](docs/architecture/0003-container-strategy.md)).
