# Runbook

Operational reference for the `shortn` services: what to check, what the system does on its own, and what an operator does when a dependency misbehaves.

Postgres is the only dependency `shortn` cannot serve writes without. When it is down, writes fail fast with a clean `503`. Redis and Redpanda are optimizations: their outages degrade the experience (slower reads, paused analytics) but never break correctness. Every failure mode below is asserted by the chaos checks in `make chaos` (`scripts/chaos.sh`), which take each dependency down in turn and verify the documented behavior.

## Health checks

Two probes, with deliberately different meanings.

- **`/healthz` — liveness.** "Am I alive? Restart me if I'm not." Cheap, with no dependency checks. It answers fast even when Postgres or Redis is down, because a failing dependency is not a reason to kill the process. A liveness probe that touches dependencies causes restart storms: every instance dies when the DB hiccups. Returns `200`.
- **`/readyz` — readiness.** "Should I receive traffic right now?" Checks Postgres only. If Postgres is unreachable it returns `503` so the load balancer holds traffic back until the instance can serve. Redis is excluded on purpose: Redis-down is degraded-but-serving, so adding it to readiness would pull a working instance out of rotation.

Kubernetes wires both: the liveness probe restarts a hung container, the readiness probe gates it in and out of the Service load balancer.

## Failure-mode table

How `shortn` behaves when each dependency fails, in SRE-playbook form. Each entry runs symptom → likely cause → diagnosis → mitigation → verification.

### Postgres unreachable or hung

- **Symptom.** Cold-cache reads and all creates return `503`. A short burst of `500`s appears during the roughly 5 failures that trip the breaker, then it settles to fast `503`. Cache hits keep redirecting (`302`). `/readyz` returns `503`, so the load balancer pulls the instance.
- **Likely cause.** Postgres process down, failing over, connection-pool exhaustion, or a hung query holding the call past its deadline.
- **Diagnosis.** `make k8s-status` / `docker compose ps` for the container state. `/readyz` returns `503` while `/healthz` stays `200` — that split is the fingerprint of a Postgres-only outage. On the dashboards, the create 5xx ratio climbs and the `CreateErrorSLOBreach` rule fires. In logs, look for `context.DeadlineExceeded` (a hang hitting the request budget) and `gobreaker.ErrOpenState` (the breaker open and fast-failing).
- **Mitigation.** Restart or fail over Postgres. No app restart needed: each instance's circuit breaker half-opens after the cooldown (~5s), runs one trial request, and re-closes on success. Retries on the idempotent read path are bounded by the request deadline, so they do not pile up.
- **Verification.** A cold-cache redirect returns `302` again and a create returns `201`. `/readyz` returns `200`. The chaos check `cache hit still redirects` and `readyz reports not-ready` both pass against `make chaos`.

### Redis unreachable (cache and rate limiter)

- **Symptom.** Redirects stay correct but slower: every read is now a cold Postgres hit. The rate limiter fails open, so limits are unenforced. Idempotency keys are unenforced, so a retried create may mint a duplicate. `/readyz` stays `200`.
- **Likely cause.** Redis process down, paused, or network-partitioned from the API instances.
- **Diagnosis.** Redirect latency rises and the redirect p99 panel climbs; `RedirectLatencySLOBreach` may fire under load. Logs show the limiter logging its degradation (fail-open) on each request. `/readyz` stays green, which distinguishes a Redis outage from a Postgres one.
- **Mitigation.** Restart Redis; the cache re-warms itself from Postgres traffic. During the outage, lean on nginx-level rate limiting as a coarse backstop. Note that with the limiter failing open and idempotency keys unenforced, retried creates are not deduped until Redis returns.
- **Verification.** Redirect latency returns to single-digit milliseconds for warm codes. A burst past the configured bucket capacity returns `429` with a sane `Retry-After` again. The chaos check `redirect still works (served from Postgres)` passes.

### Redpanda / Kafka broker unreachable

- **Symptom.** Redirects are unaffected (`302` as normal). Click events fail to publish; the publish error is logged and is non-fatal. Analytics pauses. Clicks made during the outage are lost — at-most-once on the produce side, by design.
- **Likely cause.** Broker down or stopped, or the dual-listener misconfigured so clients connect to the bootstrap address and then hang reaching the advertised one (see "Redpanda dual-listener" below).
- **Diagnosis.** Redirects keep returning `302` in a few milliseconds, which rules the broker out as a redirect-path problem. In API logs, the post-redirect publish logs an error. Consumer lag (`rpk group describe shortn-analytics`) stops advancing because no new records arrive; the `AnalyticsConsumerLagHigh` rule may fire afterward as the consumer drains a backlog. `TargetDown` fires if the `analytics` scrape target is also gone.
- **Mitigation.** Restart the broker; new clicks publish again on recovery and the consumer resumes from the offset stored in Postgres. Clicks lost during the outage are not backfilled.
- **Verification.** A round-trip produce/consume from the host succeeds: `rpk topic produce shortn.clicks` then `rpk topic consume shortn.clicks --num 1`. A real click increments `click_events`. The chaos check `redirect unaffected (publish is non-fatal)` passes.

## Redpanda operations

The broker advertises **two** listeners, and both must be set or one side hangs. A Kafka client connects to a bootstrap address; the broker then hands back the *advertised* address for real traffic. Containers on the compose network reach the broker at `redpanda:9092` (internal); the host (for `rpk` and ad-hoc testing) reaches it at `localhost:19092` (external). Advertise only one and the other side connects, gets handed an address it cannot route to, and hangs. The compose config sets:

```
--kafka-addr=internal://0.0.0.0:9092,external://0.0.0.0:19092
--advertise-kafka-addr=internal://redpanda:9092,external://localhost:19092
```

This is the single most common Redpanda-in-Docker footgun.

Create the click topic (6 partitions, idempotent — no-op if it exists):

```sh
make topics
# equivalently, from the host on the external listener:
rpk topic create shortn.clicks -p 6 -X brokers=localhost:19092
rpk topic list -X brokers=localhost:19092
```

Run any `rpk` command via `make rpk ARGS="cluster info"`. Check consumer lag with `rpk group describe shortn-analytics`. The consumer is correctness-safe across restarts: it stores the Kafka offset in the same Postgres transaction as the click insert and seeks from Postgres (not the broker) on assignment, so killing it mid-transaction loses nothing and double-counts nothing.

If Redpanda crashes on startup with an `aio-max-nr` error (exit 133) — a Docker Desktop VM limit, not a code bug — raise the limit once and bring the stack back up:

```sh
docker run --rm --privileged alpine sh -c 'echo 1048576 > /proc/sys/fs/aio-max-nr'
```

## Horizontal scaling check

Multiple API instances sit behind nginx (compose) or the Service (Kubernetes). Each tags its responses with an `X-Served-By` header carrying its instance ID. To confirm requests are spread across instances, watch that header rotate across repeated calls:

```sh
for i in $(seq 1 6); do curl -s -o /dev/null -D - localhost/healthz | grep -i x-served-by; done
```

Distinct instance IDs cycling across the responses confirms round-robin balancing is live.

## Local operations (docker compose)

`COMPOSE_FILE=deploy/compose/docker-compose.yml` (or pass `-f`).

- Start the stack — `make up` (or `make up-build` to rebuild images first). After editing Go source, rebuild: the API containers serve a built image, not live source.
- Status / logs — `make ps` · `make logs`.
- Migrations — `make migrate-up` · `make migrate-down`.
- Reload nginx — `make nginx-reload`.
- Run the analytics consumer locally — `make run-analytics`.

## Kubernetes operations

The stack runs on a local `kind` cluster (and live on Azure k3s), packaged as a Helm chart (`deploy/k8s/shortn`). One-shot bring-up of a fresh cluster: `make k8s-up`.

- Deploy / upgrade — `make helm-install` (idempotent `helm upgrade --install`).
- Status / logs — `make k8s-status` · `make k8s-logs APP=shortn-api`.
- Roll back a bad release — `helm rollback shortn` (to the previous revision).
- Tear down — `make helm-uninstall` (release; PVCs survive) · `make kind-down` (whole cluster).

### Autoscaling and the WORKER_ID lease

The API has a CPU-based HPA. Exercise it with `make k8s-load` / `make k8s-load-stop`; scale-up is fast, scale-down waits a ~5-minute stabilization window. Horizontal scale is collision-safe: each pod leases a unique Snowflake worker ID from Redis on startup (`SET shortn:worker:<n> NX EX`, held alive by a heartbeat, freed on shutdown — see `internal/idgen/lease.go` and the wiring in `cmd/api/main.go`). An explicit `WORKER_ID` env still overrides the lease (docker-compose assigns IDs by hand). Scaling to 4 pods yields worker IDs 0–3 (`shortn:worker:0..3` in Redis).

Operational notes for the lease:

- A pod that can't reach Redis at startup refuses to boot. Unlike the cache, ID assignment cannot fail open — a pod with no or duplicate worker ID is unsafe. Kubernetes restarts it until Redis is reachable.
- Residual risk: if a pod is network-partitioned from Redis longer than the ~30s lease TTL while still serving, its slot can be reclaimed by another pod, and two pods could share a worker ID. A collision only follows if both mint in the same millisecond at the same sequence, which is rare. The generous TTL plus a 10s heartbeat keep the window small; a lost lease logs at ERROR (`worker-id lease LOST — restart this pod`). True fencing would need fencing tokens, which Snowflake IDs don't carry.
- The slot pool is `maxWorkerID` = 1023, far above any replica count in practice.

### Secrets (Sealed Secrets)

The API's Secret is not committed in plaintext. The repo commits a SealedSecret (`deploy/k8s/shortn/templates/sealedsecret.yaml`): ciphertext encrypted to the in-cluster sealed-secrets controller's public key. ArgoCD deploys the SealedSecret; the controller decrypts it into the real `shortn-api-secret`. Git holds only ciphertext.

- Install the controller — `make sealed-secrets`.
- Re-seal after changing a value, then commit and push (ArgoCD syncs it):

  ```sh
  kubectl create secret generic shortn-api-secret --namespace default \
    --from-literal=DATABASE_URL='...' --from-literal=REDIS_URL='...' \
    --dry-run=client -o yaml | kubeseal --format yaml \
    > deploy/k8s/shortn/templates/sealedsecret.yaml
  ```

A SealedSecret is encrypted to *this* controller's key. `terraform destroy && apply` creates a new controller with a new key, so the committed SealedSecret can no longer be decrypted — the Secret won't be created and the API will fail. To keep "recreate from code" working, back up the controller's sealing key and restore it on the new cluster. The key is itself a secret; store it outside git.

```sh
make seal-key-backup    # writes the key to .secrets/ (gitignored) — copy it somewhere safe
make seal-key-restore   # on a fresh cluster: re-applies the key, restarts the controller
```

One-time setup of a key you control, so the committed SealedSecret survives every rebuild:

1. `make sealed-secrets` on a running cluster (controller auto-generates a key).
2. `make seal-key-backup` — save that key to `.secrets/` and stash a copy somewhere safe.
3. Re-seal the Secret against this controller (the `kubeseal` command above), commit and push.

The backed-up key and the committed SealedSecret are now a matched pair. After any `destroy`/`apply`: `make sealed-secrets` → `make seal-key-restore` → the committed SealedSecret decrypts with no re-seal. Skip the key backup and you must re-seal after each recreate.

## Backups & restore

Postgres holds the only durable state (`links`, `click_events`). Backups are logical dumps (`pg_dump`, compressed custom format). A backup that has never been restored is not a backup; the deliverable is a proven restore, so run restore drills.

### docker compose

- `make db-backup` — dump to `backups/shortn-<timestamp>.dump` (`backups/` is gitignored; dumps may contain real data).
- `make db-restore FILE=backups/<file>.dump` — restore into the running DB (drops and recreates objects).
- `make db-verify` — restore drill: load the latest dump into a throwaway DB and print live vs restored row counts. Matching counts mean a proven backup. Run it regularly.

### Kubernetes

- A CronJob `shortn-postgres-backup` (daily 03:00) runs `pg_dump` into the `shortn-backups` PVC and prunes dumps older than 7 days ([deploy/k8s/shortn/templates/postgres-backup-cronjob.yaml](../deploy/k8s/shortn/templates/postgres-backup-cronjob.yaml)).
- Trigger one now: `kubectl create job --from=cronjob/shortn-postgres-backup shortn-backup-manual`, then `kubectl logs job/shortn-backup-manual` (its last line `ls -lh /backups` lists current dumps).
- Restore: run a one-off Job that mounts the `shortn-backups` PVC and runs `pg_restore -U dev -d shortn --clean --if-exists /backups/<file>.dump` against the `shortn-postgres` service (mirror the CronJob's container and env, swap `pg_dump` → `pg_restore`). Always rehearse into a scratch DB first, the same discipline as `make db-verify`.

The k8s dumps sit on a PVC in the same cluster: they survive pod and node restarts but not a cluster loss. Production ships dumps off-cluster to object storage (S3/GCS) with lifecycle retention; point-in-time recovery (WAL archiving) is the upgrade when "everything since the last dump" is too much to lose. See [explanation/data-durability.md](explanation/data-durability.md).

## SLIs, SLOs & alerting

An SLI is an indicator the system measures. An SLO is the target for that indicator. An SLA is a contractual promise with consequences; `shortn` has no SLA (no customer contract). Alerts fire on the SLO, computed from the SLI in PromQL; the rules live in [deploy/compose/observability/alerts.yml](../deploy/compose/observability/alerts.yml).

| SLI (measured)                             | SLO (target)         | Alert rule                                                 | Expr basis                                                                                   |
| ------------------------------------------ | -------------------- | ---------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| Redirect latency — `/{code}` served fast   | 99.9% < 50 ms (30 d) | `RedirectLatencySLOBreach` (warning) — p99 > 50 ms for 5 m | `histogram_quantile(0.99, …http_server_duration_milliseconds_bucket{http_route="/{code}"}…)` |
| Create success — `POST /api/links` non-5xx | 99% non-5xx (30 d)   | `CreateErrorSLOBreach` (warning) — 5xx ratio > 1% for 5 m  | `…_count{…"5.."} / …_count`                                                                  |

Operational alerts (not SLOs, but page-worthy):

| Condition                  | Alert                                | Trigger                                 |
| -------------------------- | ------------------------------------ | --------------------------------------- |
| A service can't be scraped | `TargetDown` (critical)              | `up{job=~"shortn-.*"} == 0` for 1 m     |
| Consumer falling behind    | `AnalyticsConsumerLagHigh` (warning) | `analytics_consumer_lag > 1000` for 5 m |

Latency rules use `http_server_duration_milliseconds`, not `http_server_request_duration_seconds`: the pinned otelhttp v0.60 records ms values into the seconds-named histogram, so the ms metric is the correct one. Thresholds are therefore in ms (`50`, not `0.05`).

These are simple threshold/symptom rules. The richer form is multi-window burn-rate alerting (fast-burn plus slow-burn windows against the error budget), a future upgrade not needed at this scale.

To force a breach and confirm the rules fire (Prometheus → Alerts lists them *Inactive*):

- `make load` — the dev host saturates and redirect p99 climbs past 50 ms, so `RedirectLatencySLOBreach` goes *Pending* → *Firing* after 5 m.
- `docker compose -f deploy/compose/docker-compose.yml stop analytics` — `TargetDown` fires within 1 m. Restart with `up -d analytics`.
