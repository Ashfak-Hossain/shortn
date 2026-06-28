# Performance report — `shortn`

**Status:** current. Methodology, environment, the redirect and create workloads, the first
bottleneck (found, fixed, re-measured), and the autoscaling demonstration are all captured below.

Every number here is stated as the four-part sentence it has to be to mean anything:
**{workload} at {throughput} → {percentile} = {value}, on {environment}.** Absolute latencies come
from a single laptop with the load generator co-resident, so the trustworthy results are the
relative ones — before/after a change, and the shape of the curves — not a headline RPS. The Method
section below records the reasoning behind each measurement choice.

---

## Environment

| | |
|---|---|
| Host | Apple Silicon Mac, 8 cores, 8 GB RAM (`darwin/arm64`) |
| Docker Desktop | 6 CPUs, 4 GB memory, 2 GB swap allotted to the VM |
| System under test | `docker compose` stack: 3 API replicas (`api-1/2/3`) behind nginx (`:80`), Postgres, Redis, Redpanda, plus the observability stack (Prometheus, Grafana, Loki, Tempo, Alloy), all co-resident |
| Load generator | k6 v2.0.0, run natively on the host (competes with the stack for the same 8 cores) |
| Reached via | nginx on `http://localhost:80`, load-balancing the 3 API instances |

Caveats that shape how the numbers read:

- The whole stack and the load generator share one 8-core / 8 GB laptop. Inter-service hops are
  loopback (microseconds, not real-network ms), and CPU is contended. Absolute numbers flatter the
  system; the relative results (before/after, the knee shape) are what hold.
- The live Grafana dashboard tab was closed during every measured run. Left open, it burned ~0.6 of
  a core (Grafana render + Prometheus query + browser) and measurably inflated the tail. Dashboards
  are read after the run, from history.
- Memory is the hard ceiling. On an 8 GB host there is no room to give Docker more without starving
  macOS and native k6, so resource limits stay at 6 CPU / 4 GB and the constraint is reported rather
  than hidden.

---

## Method

A performance claim only means something as a four-part sentence: **serving {workload} at
{throughput}, the {percentile} latency is {number}, measured on {environment}.** Drop any part and
the number is noise. "It does 8,000 req/s" — at what latency? "p99 is 12ms" — at what load? "It's
fast" — doing what, where? The rest of this section is the vocabulary for each blank, and why each
choice below was made the way it was.

### Latency percentiles, and why the average lies

Latency is how long one request takes, end to end. A load test produces thousands of latencies, and
they have to be summarized — the average is the wrong summary. Suppose 100 requests: 99 take 5ms and
1 takes 2000ms (a GC pause, a cold miss, a lock). The average is ~25ms, a number no single request
experienced. It hides the 2000ms outlier behind 99 fast ones.

Percentiles describe the distribution honestly. Sort the latencies ascending; the p99 is the value
at the 99th position out of 100 — "99% of requests were at least this fast; 1% were slower."

- p50 (median) — the typical request. Half are faster, half slower.
- p95 — the slow-ish tail. 1 in 20 requests is worse than this.
- p99 — the tail latency. 1 in 100 is worse.
- p99.9 — the disaster tail. At a million requests, 1 in 1000 is 1000 people.

At scale the tail is the user experience. Serve 1,000,000 redirects at p99 = 500ms and that is
10,000 people every cycle hitting half a second. In a system that fans out — one page making 20
backend calls — a request is only as fast as its slowest call, so a 1-in-100 slow call means roughly
1 in 5 page loads is slow (`1 − 0.99^20 ≈ 18%`). Tail latency compounds, which is why this report
leads with p99 rather than the average.

One arithmetic note: percentiles don't add. A global p99 cannot be reconstructed by averaging
per-second p99s. k6 computes them from the full sample, so the `http_req_duration` p(99) it reports
is the one to trust.

### Throughput vs latency, and the knee

Throughput is the system metric: requests completed per second. Latency is the per-request metric.
They are not independent. At low load they're unrelated — send 10 req/s, each finishes in 5ms, the
system is idle between requests. Push harder and for a while throughput climbs while latency stays
flat; that is idle capacity being used. Then comes the knee: a resource saturates, requests queue
for it, and latency shoots up while throughput flattens or drops. Past the knee, adding load makes
everything worse for everyone.

The real capacity number is the throughput at the knee, with latency still under SLO — not the
throughput past it where p99 is several seconds. "Max RPS" with no latency bound is a vanity number;
"max RPS while p99 < 50ms" is an engineering number. Little's Law frames the same thing intuitively:
`concurrency ≈ throughput × latency`, so if latency rises under a fixed arrival rate, in-flight
requests pile up — exactly the queueing seen past the knee.

### Open vs closed load models

There are two ways to generate load, and they answer different questions.

Closed model — constant concurrency. A fixed number of virtual users (VUs). Each VU sends a request,
waits for the response, then sends the next. Concurrency is constant; the request rate is whatever
the system can sustain. A struggling system simply receives fewer requests, so the closed model
self-throttles and hides the knee.

Open model — constant arrival rate. A fixed arrival rate: X new requests start every second
regardless of whether previous ones finished. The number of in-flight requests is whatever results,
and if the system slows it grows without bound. This models the real world, where the internet does
not stop sending traffic because the DB is slow. Real overload and tail-latency blowup only show up
under an open model, which is why every headline number here uses one.

k6 expresses this through executors:

| Want | k6 executor | Model | Use for |
| --- | --- | --- | --- |
| Fixed arrival rate | `constant-arrival-rate` | open | realistic load, finding the knee |
| Ramp arrival rate up | `ramping-arrival-rate` | open | stress test: walk load up to the knee/SLO breach |
| Fixed looping VUs | `constant-vus` | closed | "how fast with exactly N concurrent clients?" |
| Ramp VU count | `ramping-vus` | closed | soak/spike shaped by users, not rate |

The classic mistake is using `ramping-vus` (closed) to "find max throughput," reporting a low
number, and concluding the system is slow — when the closed model throttled the load. For capacity
work, the answer is an `*-arrival-rate` executor. When k6 warns that it can't start enough VUs to
sustain the arrival rate, that warning is a real signal: the system is too slow to keep up, i.e.
past the knee.

### The cache-hit / cache-miss workload trap

A URL shortener is wildly read-heavy — people click short links far more than they create them, on
the order of 100:1. A redirect test that hits the same code every time is a 100% cache-hit test,
benchmarking Redis, not the system. A test that hits only brand-new random codes is a 100%
cache-miss test, benchmarking Postgres. Real traffic is a mix with a hot set (a Zipfian
distribution). The redirect runs here pre-create a 200-code pool and read it uniformly at random — a
mix across the pool, reproducible and stated, rather than one hot code. That pool fits in Redis, so
the observed hit ratio is ~100% and the runs measure warm steady-state reads. The limitation of that
choice is recorded below.

### Bottleneck analysis: USE / RED

When load pushes past the knee, something is the limit. Identifying which resource saturated, then
proving it from data, is the point. The dashboards from the observability stack (Grafana,
`http://localhost:3000`) and Prometheus are the instruments. The method is USE / RED, top-down:

- RED, per service: Rate (req/s), Errors (5xx ratio), Duration (latency percentiles). Tells you
  that you're slow and where.
- USE, per resource: Utilization (CPU %, pool-in-use), Saturation (run-queue, pool-wait), Errors.
  Tells you why — which resource is the wall.

Usual suspects for `shortn`, and the tell:

| Candidate | How it shows | Likely fix |
| --- | --- | --- |
| DB connection pool exhausted | latency climbs but Postgres CPU is low; requests wait for a connection (pgx acquire wait) | raise `pool_max_conns`; serve more reads from Redis |
| API CPU saturated | pod CPU pegged at its limit; HPA scales, or can't if at `maxReplicas` | raise CPU limit, or let the HPA add pods |
| Redis cold / low hit ratio | hit-ratio panel low; every read reaches Postgres | warm the cache; check the workload isn't all-miss |
| GC / allocations | sawtooth latency, CPU in GC; confirm with `pprof` | cut per-request allocations on the hot path |
| The load generator itself | client CPU pegged; k6 can't sustain the arrival rate | run k6 with more resources or off-box; never report numbers from a saturated client |

The discipline that matters: change one thing, re-measure, compare to the baseline. A before/after
with the same methodology at the same load is the most valuable artifact in this report — that is
Bottleneck #1 below.

### k6 thresholds as an SLO gate

The redirect script encodes the SLO as thresholds — `http_req_duration: p(99)<50ms` and
`http_req_failed: rate<0.01`. A breaching run exits non-zero, so a load test fails like a unit test
and can gate CI. `check(...)` per-response assertions (status is 302/201) feed a pass/fail rate, and
k6 prints p50/p90/p95/p99, RPS, and error rate at the end. This is the bridge from "a number" to "a
guarantee."

Scripts: [load/redirect.js](../load/redirect.js), [load/create.js](../load/create.js),
[load/config.js](../load/config.js). Run via `make load-redirect` / `make load-create` (override
`LOAD_RATE` / `LOAD_DURATION` / `LOAD_BASE_URL` / `LOAD_HOST`).

---

## Bottleneck #1 — nginx upstream connection churn

The symptom. Under sustained load the client-side tail latency grew over time and ran away, even
though the median stayed ~1.6ms. At a fixed 500 req/s the open model exposed true overload: p99
climbed to 1.84s, max 4.9s, k6 had to spin up 643 VUs and dropped 912 iterations just to sustain the
arrival rate — the classic "service rate < arrival rate, queue → ∞" signature. Meanwhile the API's
own p99 (Grafana, server-side) stayed at ~5ms.

The diagnosis. That ~167ms gap between what the client felt (p99 172ms at 200/s) and what the
handler actually took (~5ms) is time spent outside the Go process. The cause was in
[deploy/compose/nginx.conf](../deploy/compose/nginx.conf): the `upstream` block had no `keepalive`
directive. nginx was opening a fresh TCP connection (handshake + teardown) to an API container on
every request and closing it, piling up `TIME_WAIT` sockets — which compounded over a run,
explaining the growing tail.

The fix. Pool and reuse upstream connections:

```nginx
upstream shortn_api {
    ...
    keepalive 64;                    # pool of idle connections kept open to the API
}
location / {
    proxy_http_version 1.1;          # keepalive requires HTTP/1.1
    proxy_set_header Connection "";  # clear nginx's default "Connection: close" to upstream
    ...
}
```

All three lines are required together: upstream keepalive only works over HTTP/1.1 with the
`Connection` header cleared, since nginx sends `Connection: close` to upstreams by default, which
would defeat the pool.

The result — identical 200 req/s open load, only nginx changed:

| metric | before (no keepalive) | after (`keepalive 64`) | change |
|---|---|---|---|
| median | 1.64 ms | 1.63 ms | unchanged |
| p90 | 4.49 ms | 2.96 ms | 1.5× |
| p95 | 12.94 ms | 4.44 ms | 2.9× |
| p99 | 172.33 ms | 11.41 ms | 15× better |
| max | 423.02 ms | 135.68 ms | 3.1× |
| avg | 6.84 ms | 2.22 ms | 3.1× |
| SLO `p99 < 50ms` | FAIL | PASS | — |
| errors | 0% | 0% | — |

What it proves. The median didn't move, so the app was always fast per-request and this was never a
compute problem. The fix crushed the tail (p99 down 15×), the signature of a connection/queueing
fix. The client/server latency gap collapsed from ~167ms to ~6ms, confirming the cost was nginx
connection churn and not host contention.

The fix landed via `make nginx-reload`, which recreates the nginx container rather than reloading in
place. That is necessary on macOS Docker Desktop because an editor's atomic save swaps the file's
inode and the bind mount keeps pointing at the old one, so an in-place reload reads a stale config.

---

## Knee sweep (redirect workload)

Stepping the open-model arrival rate to find where p99 crosses the 50ms SLO. The keepalive fix
raised the throughput ceiling: 500 req/s went from a runaway queue to fully sustained.

| arrival rate | throughput (req/s) | p50 | p90 | p95 | p99 | dropped | VUs | verdict |
|---|---|---|---|---|---|---|---|---|
| 200/s (post-fix) | 201.9 | 1.63 ms | 2.96 ms | 4.44 ms | 11.41 ms | 0 | 23 | SLO pass |
| 300/s (post-fix) | 300.9 | 1.26 ms | 6.10 ms | 48.32 ms | 384.28 ms | 11 | 143 | tail noise (see below) |
| 500/s (pre-fix) | runaway | 1.39 ms | — | 406 ms | 1.84 s | 912 | 643 | past throughput ceiling |
| 500/s (post-fix) | 499.96 | 1.00 ms | 5.62 ms | 17.51 ms | 172.1 ms | 0 | 141 | throughput sustained, tail > SLO |

The keepalive fix separated two distinct knees:

- Throughput ceiling (where the queue runs away / iterations drop): was below 500 req/s, now ≥ 500
  req/s — 500/s sustains the full rate with 0 drops.
- Latency-SLO knee (where p99 crosses 50 ms): could not be cleanly pinned, for the reason below.

The tail is noise, not signal, so the SLO knee is not reliably measurable on this box. The p99 column
is non-monotonic: 300 req/s measured p99 = 384 ms while 500 req/s measured p99 = 172 ms. More load
producing a lower p99 is impossible for a real capacity curve, so the p95/p99 tail here is dominated
by sporadic contention (8 cores shared by the stack, k6, and the OS), not by load. The body of the
distribution is stable across every rate (median ~1.0–1.6 ms, p90 ~3–6 ms); only the tail jumps
around (a single 300/s request hit 1.18 s max). The defensible conclusions:

- median ≈ 1.3 ms, and throughput sustains to ≥ 500 req/s with ~0 drops;
- the tail is environment noise from co-residence, which is why this report leans on the controlled,
  same-rate before/after (Bottleneck #1) rather than an absolute knee;
- pinning a real knee would need a quieter rig (load generator off-box, observability stack off
  during the run, multiple runs reporting the distribution of p99).

The bottleneck also moved. At 200/s the API's server-side p99 is ~5 ms; at 500/s it climbs to ~122
ms (Grafana). So past ~200 req/s the limit is no longer nginx connection churn (fixed) but the stack
itself saturating CPU on the shared 8-core box. The client/server p99 gap shrank from ~167 ms
(pre-fix) to ~50 ms (post-fix), confirming the churn is gone.

---

## Create workload

`POST /api/links` → `201` — the write path (Snowflake ID generation + Postgres insert across the 3
API instances), open model, [load/create.js](../load/create.js) via `make load-create`. Each
iteration creates a unique link (URL keyed by `__VU`/`__ITER`/time) so every POST is a real insert,
not an idempotent no-op.

| arrival rate | throughput (req/s) | p50 | p90 | p95 | max | dropped | VUs | errors |
|---|---|---|---|---|---|---|---|---|
| 300/s, 30s | 299.99 | 1.37 ms | 2.62 ms | 4.04 ms | 93.25 ms | 0 | 5 | 0% |

The write path is not a bottleneck at this scale, and this run cross-validates the read-tail noise
finding: at the same 300 req/s through the same (post-fix) nginx, writes sustained the rate with only
5 VUs and a tight tail (p95 4 ms, max 93 ms), while reads needed 143 VUs and spiked to 1.18 s. A real
capacity knee at 300/s would hit both paths; that only reads spiked confirms it was transient
contention, not load. No latency-SLO threshold is set on creates — the write path has no `< 50 ms`
SLO — so k6's default summary reports through p95, not p99.

---

## Autoscaling demonstration (kind / HPA)

Environment: the kind cluster (separate from the compose measurements above), chart deployed via
Helm with `values-dev.yaml` — HPA enabled, min 1 / max 4, CPU target 50% — and metrics-server
supplying CPU. Load was driven by an in-cluster pod hammering `/healthz` through the `shortn-api`
Service (`make k8s-load`), watched with `kubectl get hpa,pods -w`. In-cluster load avoids the
host-side k6 ↔ stack contention; the goal here is to exercise the HPA, not to measure latency.

Scale-up — load on:

| stage | CPU (current / target) | replicas |
|---|---|---|
| baseline (idle) | 36% / 50% | 1 |
| load applied | 218% / 50% | 1 → 4 (immediately) |
| peak | 448% / 50% | 4 |
| 4 pods sharing the load | 280% → 231% | 4 |

- It jumped straight to the max (1 → 4), not one step at a time. The HPA's formula is
  `desired = ceil(current × currentCPU / targetCPU)` = `ceil(1 × 218/50)` = `ceil(4.36)` = 5, capped
  at `maxReplicas: 4`. CPU was so far over target it requested the ceiling at once.
- New pods reached Ready in ~8–10 s (`Pending → ContainerCreating → Running → 1/1`), fast because the
  `:dev` image is already on the node and `/readyz` passes quickly.
- CPU then fell 448% → 231% as the 4 pods absorbed the load — the payoff of autoscaling: added
  capacity measurably relieved per-pod pressure.

Scale-down — load off (`make k8s-load-stop`):

| stage | CPU | replicas |
|---|---|---|
| load removed | 291% → 223% → 87% | 4 |
| recovered | 10% → 4% | 4 (held) |
| after the ~5-min stabilization window | <50% sustained | 4 → 1 (min) |

- CPU recovered to baseline within seconds, but the HPA held at 4 pods through the default ~5-minute
  downscale stabilization window before returning to 1. This is deliberate, so a brief lull doesn't
  cause it to flap pods down and back up.

The HPA reacts within seconds to CPU pressure, scales out to add capacity that demonstrably reduces
per-pod load, and scales back in conservatively: load up → pods 1→4 → per-pod CPU down → load off →
pods 4→1.

Horizontal scale is collision-safe. Every API pod once shared `WORKER_ID=0`, which made
`maxReplicas > 1` a scaling demo rather than production-safe, since the Snowflake generator needs a
unique worker id per pod. Each pod now leases a unique worker id from Redis on startup (`SET NX` +
heartbeat, `internal/idgen/lease.go`). Verified on this cluster: scaling to 4 pods produced worker
ids 0, 1, 2, 3 (`shortn:worker:0..3` in Redis). See the
[runbook](runbook.md#autoscaling--the-worker_id-lease).

---

## Limitations & what changes at 10×

Limitations of this measurement, so the numbers are read correctly:

1. Single-laptop, load generator co-resident. k6, the stack, the observability stack, and the OS all
   share 8 cores / 8 GB; networking is loopback. Absolute latencies flatter the system and the tail
   is dominated by contention (the p99 came out non-monotonic, 300/s worse than 500/s). Trust the
   relative results (same-rate before/after) and the shapes, not the absolutes.
2. Warm-cache workload. The 200-code pool fits entirely in Redis, so the redirect runs measured a
   ~100% cache-hit steady state. Production traffic is a Zipfian mix over a keyspace far larger than
   the cache — more cold misses, more Postgres pressure — which this doesn't exercise.
3. The SLO knee couldn't be pinned on this rig: run-to-run tail variance exceeded the load signal. A
   real knee needs a quieter setup.
4. Single Postgres (one instance, one PVC): all writes and cold reads funnel to one node — no
   replicas, no sharding.

What changes to take it to 10× and trust the numbers:

- Measure off-box. Move the load generator to a separate machine and run the stack on real nodes;
  run each rate multiple times and report the distribution of p99; turn the observability stack off
  during the measured run. That alone would make the knee measurable.
- Tune the HPA on a better signal. Redirect CPU is tiny, so CPU-based scaling is coarse — scale on
  RPS or p99 via custom/external metrics, with requests/limits set from these runs.
- Scale the data layer. PgBouncer pooling, Postgres read replicas for the read path, and
  hash-partitioning the links table by `code` once one node's write throughput is the wall.
- Shed load before the origin. A negative cache / Bloom filter for unknown codes (rejects bogus
  lookups in O(1)), and pushing the redirect hot path to the edge (CDN / edge workers) so cache hits
  never reach the cluster — the real-world shape for a shortener.
- Find in-process hot paths. Once the infra bottlenecks are gone, profile under load with `pprof`
  (CPU/heap flame graphs) to chase the next bottleneck inside the Go process.

The first bottleneck found here (nginx connection churn) was infrastructure, not code — at this scale
the wins are usually in how the pieces are wired, not in the handler. The next ones (DB fan-in, ID
coordination, edge caching) are the 10× story above.

---

## See also

- [docs/runbook.md](runbook.md#slis-slos--alerting) — the SLOs these k6 thresholds encode, and the
  WORKER_ID autoscaling note.
- [explanation/observability.md](explanation/observability.md) — the dashboards used to find the
  bottleneck; golden signals, SLI/SLO.
