# shortn

A distributed URL shortener built as a practice project of distributed systems and DevOps concepts.

**Stack:** Go · PostgreSQL · Redis · Redpanda (Kafka) · nginx · Docker · GitHub Actions · Prometheus/Grafana/Loki/Tempo (OTel) · Kubernetes/Helm/ArgoCD · Terraform · React

## Status

A distributed, event-driven URL shortener. `POST /api/links` returns a short code; `GET /{code}` 302-redirects — served from a Redis read-through cache (Postgres on a miss), behind an **nginx** load balancer across multiple stateless API instances. Short codes come from a coordination-free **Snowflake-style** generator and are obfuscated with **sqids** so they're non-sequential. Each click publishes a `LinkClicked` event to **Redpanda** (Kafka API) and returns immediately; a separate `cmd/analytics` consumer drains the log into Postgres with **exactly-once processing** — the Kafka offset is committed in the same transaction as the click, so a crash or restart never loses or double-counts. Clean layered architecture (`http` → domain → `store`); the cache and event publisher sit behind interfaces so the domain never learns Redis or Kafka exists, and cache/broker failures fail open. Unit + integration (testcontainers) tests, green CI; runs with `docker compose up`.
Since then the system gained **resilience** (Phase 5 — timeouts, a Redis-backed distributed rate limiter, a circuit breaker, idempotency keys, chaos-tested failure modes) and full **observability** (Phase 6 — metrics/logs/traces via OpenTelemetry, Grafana golden-signal dashboards, one click traceable end-to-end across the queue, SLO alerts; see [Observability](#observability) below).
Next: **Phase 7 — Kubernetes, GitOps & infrastructure-as-code** (kind, Helm, ArgoCD, Terraform).

## Run

Requires Go 1.26+, Docker, and `make`.

### Full stack (API + Postgres + Redis) via Docker Compose

```sh
docker compose -f deploy/compose/docker-compose.yml up --build
```

This starts Postgres and Redis, applies migrations, and serves the API on `:8080`.

### API examples

```sh
# create a short link
curl -s -X POST localhost:8080/api/links -d '{"url":"https://example.com"}'
# → {"code":"Ab3xK9p","short_url":"http://localhost:8080/Ab3xK9p","long_url":"https://example.com"}

# follow it — 302 redirect to the original URL
curl -i localhost:8080/Ab3xK9p

# errors use a consistent shape: {"error":"..."}
curl -i localhost:8080/unknown                                    # 404
curl -i -X POST localhost:8080/api/links -d '{"url":"not-a-url"}' # 400
```

Health & readiness:

```sh
curl -i localhost:8080/healthz   # 200 — liveness (dependency-free)
curl -i localhost:8080/readyz    # 200 — readiness (checks the database)
```

### Local development

```sh
make run          # run the API (needs a reachable Postgres via DATABASE_URL)
make test         # go test ./...  (unit tests)
make lint         # go vet + golangci-lint
make migrate-up   # apply migrations
make migrate-down # roll back the last migration
make docker       # build the container image (< 30MB)

# integration tests spin a real Postgres via testcontainers (needs Docker):
go test -tags integration ./...
```

Config is read from the environment: `PORT`, `LOG_LEVEL`, `ENV`, `DATABASE_URL`, `REDIS_URL`.

## Deploy (Kubernetes)

The whole stack runs on a local **kind** cluster, packaged as a **Helm** chart
(`deploy/k8s/shortn`), delivered by **ArgoCD** (GitOps), with the cluster and ArgoCD install
themselves declared in **Terraform** (`deploy/terraform`). The API's Secret is committed only
as an encrypted **SealedSecret** — never plaintext. Every `make` target for this is in the
[Makefile](Makefile); the full walkthrough is [docs/phases/phase-7.md](docs/phases/phase-7.md).

**Quick bring-up (Helm CLI):**

```sh
make k8s-up        # kind + ingress + metrics-server + sealed-secrets + images + chart + migrations
make k8s-status    # watch pods settle
curl -H "Host: shortn.localhost" localhost/healthz   # 200 through the ingress
```

**GitOps bring-up (Terraform builds the platform, ArgoCD deploys the app):**

```sh
make tf-init && make tf-apply        # Terraform: kind cluster + ArgoCD
kind export kubeconfig --name shortn # point kubectl at the new cluster
make sealed-secrets && make kind-load && make argocd-app && make k8s-migrate
```

What it demonstrates:

- **GitOps** — ArgoCD continuously reconciles the cluster to git; manual drift **self-heals**, a merge that changes the chart deploys itself ([ADR 0012](docs/architecture/0012-gitops-delivery.md)).
- **Zero-downtime rolling updates** — readiness-gated surge + graceful drain + a post-`SIGTERM` delay that beats the endpoint-deregistration race (zero 5xx under load).
- **Autoscaling** — an HPA scales the API on CPU (`make k8s-load` to exercise it).
- **Stateful data survives** — Postgres on a PVC outlives pod restarts; the API/analytics are stateless ([ADR 0011](docs/architecture/0011-orchestration.md)).
- **Secrets** — SealedSecrets keep only ciphertext in git; the controller decrypts in-cluster ([runbook](docs/runbook.md#secrets-sealed-secrets)).
- **CI → registry** — `.github/workflows/ci.yml` builds multi-arch images for `api` + `analytics` and pushes them to **GHCR** on every merge to `master` ([ADR 0013](docs/architecture/0013-infrastructure-as-code.md)).

## Architecture

Clean, layered design with dependency inversion:

- `internal/http` — chi handlers, validation, the consistent JSON error shape. Knows HTTP, not SQL.
- `internal/shortener` — core domain (create/resolve, URL rules, collision-retry). Defines the `LinkStore` and `IDGenerator` interfaces; imports no pgx and no `net/http`.
- `internal/store` — pgx Postgres repository implementing `LinkStore`. The only package with SQL.
- `internal/idgen` — `RandomBase62` implementing `IDGenerator` (Phase 3 swaps in a distributed scheme behind the same interface).

The domain depends on interfaces, not concrete I/O, so implementations swap without touching business logic. Decisions are recorded as [ADRs](docs/architecture/README.md).

## Performance

Redis read-through cache (cache-aside) in front of Postgres. To show the
effect, the same 500 freshly-created codes are resolved twice back-to-back over one reused
HTTP connection: the **cold** pass is a cache miss (Redis miss → Postgres → populate), the
**warm** pass is a cache hit (served from Redis, Postgres untouched).

| metric | cold (miss) | warm (hit) | speedup |
| ------ | ----------- | ---------- | ------- |
| mean   | 176 µs      | 100 µs     | 1.76×   |
| p90    | 489 µs      | 307 µs     | 1.59×   |
| p99    | 653 µs      | 378 µs     | 1.73×   |

Measured on loopback (`docker compose` on one machine), so absolute latencies are
sub-millisecond and the median sits below `curl`'s timer resolution — the meaningful figure
is the consistent ~1.7× reduction at the mean and tail. The bigger production win isn't this
local microsecond delta: a cache **hit never touches Postgres** (verified by resolving a
cached code with Postgres stopped), so the database is shielded from the read-heavy redirect
path and from hot-key stampedes (collapsed via `singleflight`). Cache failures **fail open** —
a Redis outage degrades latency, never correctness. Realistic load numbers, where the DB
carries network latency and contention, come with the k6 suite in Phase 8.

## Observability

Every service emits the **three pillars** of telemetry, instrumented once against the
**OpenTelemetry** Go SDK and exported **directly** to the backends — no OpenTelemetry
Collector in the middle (justified for two services in [ADR 0010](docs/architecture/0010-observability.md)):

| Pillar      | Answers                              | Tool           | How it's wired                                                                                                           |
| ----------- | ------------------------------------ | -------------- | ------------------------------------------------------------------------------------------------------------------------ |
| **Metrics** | _Is it healthy? Alert me._           | **Prometheus** | App exposes `/metrics`; Prometheus **pulls** (scrapes) every instance every 15s.                                         |
| **Logs**    | _What happened to this one request?_ | **Loki**       | App keeps writing JSON to stdout; **Grafana Alloy** tails containers and ships to Loki. The app never learns about Loki. |
| **Traces**  | _Why was this slow?_                 | **Tempo**      | App **pushes** spans over OTLP straight to Tempo.                                                                        |

**Grafana** (http://localhost:3000) is the single pane: it queries all three datasources and
auto-loads the provisioned **golden-signals dashboard** (Rate / Errors / Duration for the
redirect & create paths, plus cache-hit ratio, consumer lag, and saturation).

### The end-to-end trace

The showpiece: **one click produces one trace that spans both services.** The W3C
`traceparent` rides in the HTTP request, then is injected into the **Kafka record headers**
by the producer and extracted by the consumer — so the span tree runs edge → API handler →
Redis/Postgres → Kafka publish → **analytics consumer** → the click `INSERT` + offset commit,
all stitched into a single trace even though a queue breaks the in-process call stack.

![End-to-end trace spanning shortn-api and shortn-analytics across Kafka](docs/images/trace-end-to-end.png)

> _Open Grafana → Explore → Tempo, run a search, and pick a redirect trace; it carries spans
> from both `shortn-api` and `shortn-analytics`, with the queue wait visible as the gap._

### SLOs & alerting

Two SLOs drive the Prometheus alert rules ([alerts.yml](deploy/compose/observability/alerts.yml)),
documented in the [runbook](docs/runbook.md#slis-slos--alerting):

| SLI                                | SLO           | Alert                      |
| ---------------------------------- | ------------- | -------------------------- |
| Redirect latency (`/{code}`)       | 99.9% < 50 ms | `RedirectLatencySLOBreach` |
| Create success (`POST /api/links`) | 99% non-5xx   | `CreateErrorSLOBreach`     |

Plus operational alerts: `TargetDown` (a scrape target unreachable) and
`AnalyticsConsumerLagHigh` (the consumer falling behind).

### Try it

```sh
make up                                   # whole stack incl. Prometheus/Grafana/Loki/Tempo/Alloy
open http://localhost:3000                # Grafana — the shortn-overview dashboard
open http://localhost:9090/targets        # Prometheus — every scrape target up?
make load                                 # k6 traffic to make the dashboards move
```

API and observability checks are also a **Postman collection** — see [docs/postman/](docs/postman/).
