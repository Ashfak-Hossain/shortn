# Observability stack & telemetry export (0010)

**Status:** Accepted, 2026-06-16

## Context

`shortn` is distributed (N API replicas behind nginx, an async analytics consumer,
Postgres/Redis/Redpanda) and resilient. Without telemetry it is opaque: when a
redirect is slow or a click goes missing, there is no way to see where the time went
or which hop failed. Structured JSON logs answer "what happened to this one request"
but cannot answer "is the system healthy" (no aggregate metrics) or "why was this
request slow across services" (no traces).

The stack provides the three pillars — metrics, logs, traces — for every service, with
Grafana dashboards on the golden signals and a single click traceable end-to-end from
the edge through the queue into the consumer: Prometheus, Grafana, Loki, Tempo,
instrumented via the OpenTelemetry Go SDK. The decision here is how telemetry leaves
the app and reaches those backends — directly, or through a central OpenTelemetry
Collector.

## Decision

1. **Direct export, no Collector.**
   - Metrics — the app exposes a Prometheus `/metrics` endpoint via the OTel SDK
     Prometheus exporter; Prometheus scrapes each replica (the pull model).
   - Traces — the app pushes spans over OTLP/HTTP straight to Tempo.
   - Logs — the app writes JSON to stdout; Grafana Alloy tails the container logs and
     ships them to Loki. The app has no knowledge of Loki.

2. **Instrument once with OpenTelemetry**, at the boundaries only: `otelhttp` for
   inbound HTTP (and W3C `traceparent` propagation), `otelpgx` for Postgres,
   `redisotel` for Redis, and `kotel` (the franz-go plugin) for Kafka, which also
   injects/extracts `traceparent` into Kafka record headers, stitching the API and
   the analytics consumer into one trace. The core domain packages
   (`internal/shortener`) stay free of telemetry imports, preserving the dependency
   direction set in [ARCHITECTURE.md](../../ARCHITECTURE.md).

3. **One bootstrap package** (`internal/observability`) builds the `Resource`,
   `TracerProvider`, Prometheus `MeterProvider`, and sets the global
   `TraceContext` propagator; both `cmd/api` and `cmd/analytics` reuse it. Sampler is
   `AlwaysSample` in dev, ratio-based (config-driven) in prod.

4. **Cardinality discipline is a rule, not a guideline.** Metric labels are bounded
   (route template `/{code}`, method, status class). High-cardinality identifiers (the
   short code, the URL) live on span attributes or log lines, never metric labels.

## Alternatives considered

- **OpenTelemetry Collector in the middle** (app → OTLP → Collector → Prometheus/Tempo/
  Loki), the "instrument once, export anywhere" topology. Rejected: with only two
  services it is a box that adds a container and config while buying nothing. Its payoff
  — decoupling the app from backends, tail-based sampling, fan-out to many exporters —
  arrives with many services. It can be added then without app code changing, because
  export config moves out, not in.
- **Push metrics via a Pushgateway** — rejected: the Pushgateway is for short-lived batch
  jobs, not long-running replicas; using it for a scraped service loses per-instance
  liveness and creates a stale-metric trap. Prometheus pull is the correct model here.
- **App ships its own logs to Loki** (e.g. a Loki slog handler) — rejected: couples the
  app to the log backend and risks blocking the request path on a logging sink. Tailing
  stdout with Alloy keeps the app a 12-factor citizen (logs to stdout, someone else
  routes them).
- **Trace ID inside the Kafka JSON payload** — rejected: couples the event schema to
  tracing. Trace context belongs in headers (the W3C standard), beside the value.
- **Jaeger instead of Tempo** — rejected: Tempo integrates natively with the Grafana/
  Loki/Prometheus stack already chosen and stores traces in cheap object storage; one
  UI (Grafana) for all three pillars.

## Consequences

**Easier:**

- One UI (Grafana) for metrics, logs, and traces, with trace↔log correlation by trace ID.
- Context propagation pays off: because every external call already takes `ctx`, adding
  spans is purely additive.
- Fewer moving parts than a Collector topology, so the end-to-end trace works with less
  configuration.

**Harder / what this owes:**

- Each backend is wired directly, so a backend swap touches app config (the Collector
  would have absorbed that). Accepted at two services.
- Cardinality is policed by hand in code review; a careless label can OOM Prometheus.
- Alloy needs the Docker socket mounted (`/var/run/docker.sock:ro`) to discover
  containers, a dev-only convenience; in Kubernetes this becomes a DaemonSet reading the
  node's logs instead.
- Sampling is off in dev (100%); a sampling decision is required before any high-volume
  run so trace storage doesn't grow unbounded.

This record supersedes nothing. If a Collector is introduced, a new ADR will supersede
decision (1) above.
