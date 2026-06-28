# Observability architecture

`shortn` is distributed: N API replicas sit behind nginx, an async analytics consumer
drains a Redpanda topic, and Postgres, Redis, and Redpanda back the whole thing. With
only structured logs and `curl`, three operational questions have no answer at scale: is
the system healthy right now across all replicas, why was *this* request slow and on
which hop, and where did *this* click die between the API and the analytics row. The
observability stack exists to answer those. This document explains how the pieces fit;
the decision and the alternatives considered live in
[ADR 0010](../architecture/0010-observability.md), and the service-and-port inventory in
[ARCHITECTURE.md](../../ARCHITECTURE.md).

## The three pillars

Each kind of telemetry answers a question the other two cannot.

**Metrics** are numbers sampled over time — a counter, a gauge, or a histogram. They are
pre-aggregated, so a counter ticking from 4 million to 5 million costs the same storage
as one ticking from 4 to 5. That bounded cost is why metrics drive dashboards and alerts
and can be kept for a year cheaply. They answer aggregate-health questions — rate, error
percentage, latency percentiles, saturation — but they throw away per-event detail. A
single histogram (`http_server_duration_milliseconds`) yields redirect rate, error rate,
and p50/p99 latency across all replicas.

**Logs** are discrete timestamped records of one event. The app emits these via `log/slog` with a JSON handler, one structured line per event on stdout. They
keep the detail metrics discard — the full error string, the exact partition and offset —
at a cost that grows linearly with traffic. When a click goes missing, the consumer's
`processing failed; exiting to preserve exactly-once` line, with its partition and offset,
is what pins down where.

**Traces** are the story of one request as a tree of spans, each span an operation with a
name, start time, duration, status, and parent. A trace shows the waterfall — handler
1.9s, of which cache-get 1ms and db-query 1.85s — so the slow hop is visible directly,
where a metric only says "p99 is high." Traces carry high-cardinality detail fine because
they are not pre-aggregated; the cost is per-request, so production samples them.

Reach for metrics to know if the system is healthy, traces to know why a request was slow,
logs to know exactly what happened to one event. Grafana stitches the three: a metric
alert leads to a slow trace, which leads to that request's logs.

## The tools

Four storage-and-UI tools, one in-app library, and one agent. Each owns one job, and the
wiring is either pull or push.

**Prometheus** (`prom/prometheus:v3.12.0`, port `9090`) is the metrics database. It pulls:
the app exposes `GET /metrics` as plain-text exposition format, and Prometheus scrapes each
configured target every 15s. Pull means Prometheus holds the list of who *should* exist and
notices when a replica stops answering — a target flipping from `up` to `down` is itself a
signal — and the app stays dumb, exposing a number without knowing Prometheus's address. A
metric is a name plus labels; each unique label combination is a separate time series.
PromQL queries them: `rate(..._count[5m])` for traffic, a 5xx-over-all ratio for errors,
`histogram_quantile(0.99, ...)` for p99.

The hard constraint is cardinality. Prometheus keeps one time series per unique label
combination, so a bounded label like `http_route="/{code}"` (a handful of values) is fine,
but the short code as a label mints a new permanent series per link — a million links, a
million series, and Prometheus runs out of memory. High-cardinality detail belongs on a
span attribute or in a log line, never a metric label.

**Loki** (`grafana/loki:3.7.2`, port `3100`) is the logs database, built to avoid the cost
of a full-text engine. It indexes only labels, grouping logs into streams keyed by a small
low-cardinality label set (`{service="api", container="api-1"}`), and stores the line text
as compressed chunks. A query selects a stream by label (cheap, indexed), then greps within
it. Loki is push-based; the app does not push to it directly. The same cardinality rule as
Prometheus applies to its labels.

**Tempo** (`grafana/tempo:2.10.7`, query `3200`, OTLP `4317` gRPC / `4318` HTTP) is the
traces database. It receives spans over OTLP, the OpenTelemetry wire protocol, pushed by
the app. Traces are pushed, not pulled, because a metric is a current value readable on
demand at scrape time, while a trace is an event that already happened and is gone — there
is nothing to read later, so the app emits it when it happens. Tempo indexes primarily by
trace ID; a trace is reached by ID handed over from a log or metric, or via TraceQL.

**Grafana** (`grafana/grafana:11.6`, port `3000`) stores nothing. It queries the three
backends and draws panels. Data sources and dashboards are provisioned from YAML/JSON in
git so they exist on boot, the same reproducible-and-reviewable philosophy as migrations.
Correlation is configured so a span jumps to its surrounding logs and a log line with a
trace ID jumps to its trace, making the alert-to-trace-to-logs chain clickable.

**Grafana Alloy** (`grafana/alloy:v1.17.0`) is the log-shipping agent, and the reason the
app never imports a Loki client. Its pipeline has four stages: discover running containers
via the mounted Docker socket; relabel Docker metadata into clean low-cardinality
`service`/`container` labels; tail each container's stdout; write the lines to Loki at
`/loki/api/v1/push`. The app prints JSON to stdout in 12-factor style and stays ignorant of
Loki — if Loki is down, the app never notices. (Under Kubernetes the same role becomes a
DaemonSet reading node log files; the concept is identical.)

**OpenTelemetry (OTel)** is the in-process library that actually produces metrics and
traces, instrumented once against a vendor-neutral API so the export target is a `main()`
config decision, swappable without touching the code that produced the signal. The bootstrap
lives in [`internal/observability/observability.go`](../../internal/observability/observability.go)
and serves both binaries. It builds a `Resource` (the per-process identity —
`service.name=shortn-api`, `service.instance.id` from the replica's instance ID — stamped on
every signal), a `TracerProvider` wired to an OTLP/HTTP trace exporter pointed at Tempo, and
a `MeterProvider` wired to the OTel Prometheus exporter. That metric exporter is a Reader
that publishes OTel metrics into a private (non-global) Prometheus registry, which `promhttp`
serves at `/metrics` — so OTel produces the metrics but they still leave by the pull model.
The trace exporter connects lazily: `New()` does not dial Tempo, so a down Tempo never blocks
startup; spans simply fail to export. Tracing is never load-bearing.

## Push versus pull

| Signal  | Tool       | Direction | Mechanism                                            |
| ------- | ---------- | --------- | ---------------------------------------------------- |
| Metrics | Prometheus | pull      | Prometheus scrapes the app's `GET /metrics` every 15s |
| Traces  | Tempo      | push      | app sends spans over OTLP to Tempo `:4317`/`:4318`   |
| Logs    | Loki       | push      | Alloy tails container stdout, pushes to Loki `:3100` |
| View    | Grafana    | pull      | Grafana queries all three backends on demand         |

Telemetry export is **direct**: the app talks to each backend itself rather than routing
everything through an OpenTelemetry Collector. A Collector earns its keep across many
services — it decouples apps from backends and enables tail-based sampling (deciding which
traces to keep after seeing the whole trace) — but for two services it adds a component
without buying anything. ADR 0010 records that choice.

## The golden signals

When the question is what to measure, the four golden signals are latency, traffic, errors,
and saturation. For a request-driven service these collapse to RED — Rate, Errors, Duration
— which the provisioned `shortn-overview` dashboard leads with, all three derived from the
one HTTP histogram. Resources like the pgx pool follow USE — Utilization, Saturation, Errors.
Two domain panels sit alongside: cache hit ratio and analytics consumer lag.

Cache hit ratio is computed in PromQL, not stored. The cache layer keeps `cache.requests` as
a counter pair tagged `result="hit"` / `result="miss"`
([`internal/cache/store.go`](../../internal/cache/store.go)), surfacing as
`cache_requests_total{result="..."}` — OTel's `.` becomes Prometheus's `_`, and a counter
gains `_total`. The ratio is `hits / (hits + misses)` at query time, because a ratio cannot
be averaged across instances or over time.

SLOs drive the alert rules in `alerts.yml`: redirect p99 over 50ms for 5m, `POST /api/links`
5xx ratio over 1% for 5m, any `shortn-*` target down for 1m, consumer lag over 1000 for 5m.
An SLI is a measured indicator, an SLO the target alerted on, an SLA a contract with
penalties — `shortn` has no customers under contract, so it has SLOs and no SLA.

## Trace propagation: one click, one trace, across the queue

A trace is one connected tree only if every hop knows the trace ID and its parent span.
Within a process, Go's `context.Context` carries that automatically: `ctx` is threaded
through every call, which makes adding child spans purely additive. Across
a network boundary the context must be serialized into the message and read back on the far
side.

Over HTTP the carrier is the W3C `traceparent` header,
`version-traceID(32 hex)-parentSpanID(16 hex)-flags`. The `otelhttp` handler is the
outermost middleware in [`internal/http/router.go`](../../internal/http/router.go): it must
wrap everything so the server span covers all middleware time and the trace context exists
before anything else runs. It reads inbound `traceparent`, starts the server span, records
the duration histogram, and a `WithFilter` excludes `/healthz`, `/readyz`, and `/metrics`,
which fire constantly and would drown real traffic. Outbound child spans come from
`otelpgx` (every Postgres query) and `redisotel` (every Redis call), wired in `cmd/api`.

The queue is the case that breaks the call stack. When the API publishes a click and
returns, the in-process stack ends; the analytics consumer picks the record up later, in a
different process, with no shared `ctx` and no HTTP header. The trace ID survives only if the
producer writes it onto the message as data — specifically into the Kafka record headers, the
key/value metadata riding beside the value — and the consumer reads it back. The
`traceparent` goes in the headers, never the JSON payload, which would couple the event schema
to tracing.

```
Kafka record:
  key:     "abc123"
  value:   {"code":"abc123","ts":...}        ← LinkClicked JSON, unchanged
  headers: traceparent=00-4bf9...-00f0...-01 ← trace context rides here
```

The `kotel` plugin (franz-go's OTel integration) does the work using the global
`TraceContext` propagator set during bootstrap. On the producer side
([`internal/events/events.go`](../../internal/events/events.go)) its hook opens a publish span
and injects `traceparent` into the outgoing record headers. One subtlety in
[`internal/http/links.go`](../../internal/http/links.go): the click is published in a goroutine
*after* the redirect is on the wire, so the publish uses `context.WithoutCancel(r.Context())`.
The bare request context would be cancelled the instant the handler returns and the publish
would die; `context.Background()` would survive but lose the trace context and orphan the
publish into a separate trace. `WithoutCancel` keeps the trace context and drops only the
cancellation, which is why the publish span joins this redirect's trace. On the consumer side
([`cmd/analytics/main.go`](../../cmd/analytics/main.go)), `kotel`'s `WithProcessSpan(rec)`
extracts `traceparent` from the headers and starts the processing span as a child of the
publish span; the DB insert and offset commit nest inside it. The result is one trace
spanning `shortn-api` and `shortn-analytics`, with the queue wait visible as the gap between
them. Without the global propagator set, both halves would be disconnected traces — the
classic distributed-tracing bug.

Sampling is `AlwaysSample` in this dev configuration — every trace is kept. Production would
sample a fraction (head-based, decided at request start by the SDK) while always keeping
errors and slow requests, because trace storage grows linearly with traffic.

## A note on the latency metric units

There is a real gotcha to carry. The pinned `otelhttp` runs in `http/dup` mode
(`OTEL_SEMCONV_STABILITY_OPT_IN: http/dup` in the compose env), which emits both the legacy
and current HTTP semconv metrics. In this version that opt-in writes **millisecond** values
into the **seconds-named** histogram `http_server_request_duration_seconds`. So every latency
panel and alert uses `http_server_duration_milliseconds` instead, with millisecond units and
millisecond thresholds — the redirect SLO is `50`, not `0.05`. A panel showing "2.83s" for a
3ms request is this bug surfacing on the wrong metric. The proper fix is upgrading otelhttp and
dropping the `http/dup` env; until then, latency queries must use the `_milliseconds` metric.

## The compose observability stack

`make up` starts five containers alongside the app, all in the single growing
`docker-compose.yml`, each reading config from `deploy/compose/observability/`:

| Container    | Role                                          | Port               | Config                                   |
| ------------ | --------------------------------------------- | ------------------ | ---------------------------------------- |
| `prometheus` | scrapes `/metrics`, evaluates alert rules     | 9090               | `prometheus.yml` + `alerts.yml`          |
| `grafana`    | single UI over all data sources               | 3000               | `grafana/provisioning/**`                |
| `loki`       | stores logs                                   | 3100               | `loki-config.yaml`                       |
| `tempo`      | stores traces, opens OTLP receivers           | 3200 + 4317/4318   | `tempo.yaml`                             |
| `alloy`      | tails container stdout, ships to Loki         | 12345 (UI)         | `alloy/config.alloy` + Docker socket     |

`prometheus.yml` is the pull model as config: `scrape_interval: 15s`, with jobs naming
targets by compose service name (`api-1:8080`, `api-2:8080`, `api-3:8080`, `analytics:8080`)
since all containers share the compose network. `tempo.yaml` opens the OTLP gRPC `4317` and
HTTP `4318` receivers and writes trace blocks to a local volume with 24h retention.
`loki-config.yaml` runs single-binary mode with filesystem storage. `alloy/config.alloy`
chains the discover-relabel-tail-write pipeline. Grafana's provisioned data sources point at
the in-network URLs and wire the trace-to-logs and log-to-trace correlation.

Two env vars turn instrumentation on for the app containers:
`OTEL_EXPORTER_OTLP_ENDPOINT: tempo:4318` (where to push traces — the localhost default is
wrong inside a container) and `OTEL_SEMCONV_STABILITY_OPT_IN: http/dup` (the dual-emit mode
behind the units note above). Alloy runs as root with `/var/run/docker.sock` mounted
read-only to reach the Docker daemon; Grafana enables anonymous auth. Both are dev-only
conveniences.
