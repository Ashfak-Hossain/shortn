# Services and ports

The processes that make up the local stack, the ports they listen on, and what each one is
for. This describes the full Docker Compose stack (`deploy/compose/docker-compose.yml`); the
live deployment runs a lean subset (see [deployment](../deployment.md)).

## Application services

| Service | Image | Purpose | Host port | In-container |
| --- | --- | --- | --- | --- |
| `nginx` | `nginx:alpine` | Reverse proxy and load balancer; the local entry point | `80` | `80` |
| `api-1`, `api-2`, `api-3` | built from `Dockerfile` (`SERVICE=api`) | The API: create, redirect, stats, management | — (behind nginx) | `8080` public, `9090` ops |
| `analytics` | built from `Dockerfile` (`SERVICE=analytics`) | Drains the click event log into Postgres | — | — |

The three API instances are named services rather than `deploy: replicas`, each with a
distinct `INSTANCE_ID`, so round-robin balancing and per-instance identity are visible in the
`X-Served-By` response header. They are not published directly; nginx on `:80` is the entry
point. Each API process also serves an operations port (`9090` by default) carrying
`/healthz`, `/readyz`, and `/metrics`, which the ingress never exposes.

## Datastores and broker

| Service | Image | Purpose | Host port | In-container |
| --- | --- | --- | --- | --- |
| `postgres` | `postgres:16` | Source of truth: links and click counts | `5432` | `5432` |
| `redis` | `redis:8-alpine` | Cache, rate-limiter buckets, idempotency keys, worker-id leases | `6379` | `6379` |
| `redpanda` | `redpandadata/redpanda` | Kafka-API event log for clicks | `19092` (external), `9644` (admin) | `9092` internal, `19092` external |
| `migrate` | `migrate/migrate` | Applies SQL migrations once at startup, then exits | — | — |

Redpanda advertises two listener addresses: containers on the Compose network reach it at
`redpanda:9092`, while host tools (`rpk`) reach it at `localhost:19092`. Advertising only one
makes the other side connect and then hang — the most common Redpanda-in-Docker mistake.

## Observability stack

| Service | Image | Purpose | Host port |
| --- | --- | --- | --- |
| `grafana` | `grafana/grafana` | Dashboards over all three datasources | `3000` |
| `prometheus` | `prom/prometheus` | Scrapes each instance's `/metrics` | `9090` |
| `tempo` | `grafana/tempo` | Trace store; receives OTLP from the apps | `3200` (query), `4317` (OTLP gRPC), `4318` (OTLP HTTP) |
| `loki` | `grafana/loki` | Log store | `3100` |
| `alloy` | `grafana/alloy` | Tails container logs and ships them to Loki | `12345` (own UI) |

Prometheus is published on the host at `localhost:9090`. This is unrelated to the API's
in-container ops port, which also happens to be `9090`: Prometheus reaches the API instances
across the Compose network as `api-1:9090`, `api-2:9090`, and `api-3:9090`.

## Application ports, by configuration

The API's own ports come from the environment (see [`.env.example`](../../.env.example)):

| Variable | Default | What it controls |
| --- | --- | --- |
| `PORT` | `8080` | Public API and redirects (routed by the proxy) |
| `OPS_PORT` | `9090` | Internal-only health, readiness, and metrics |

## See also

- [Deployment topology](../deployment.md) — how these map onto the live cluster.
- [Runbook](../runbook.md) — health checks, failure modes, and operational procedures.
- [Architecture](../../ARCHITECTURE.md) — how the pieces fit together.
