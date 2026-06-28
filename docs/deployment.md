# Production deployment

`shortn` runs live at [https://shortn.ashfak.dev](https://shortn.ashfak.dev). This document
describes the production topology that actually ships: where each component runs, how a request
reaches a pod, what is locked down, and how the system is operated from a laptop.

The deploy target moved during the project. Oracle Cloud's Always-Free ARM tier was capacity-walled
(creating an instance returned "out of host capacity" in the regions tried), so production runs on
an Azure VM instead. Nothing about the application changed; only the host did.

## Topology

```
Browser
  │  HTTPS
  ▼
Cloudflare (orange-cloud proxy)        terminates TLS, absorbs DDoS, hides origin IP
  │  HTTP/HTTPS to origin
  ▼
Azure VM  (2 vCPU / 4 GB)              single-node k3s
  │
  ▼
Traefik ingress (k3s built-in)         routes by path
  ├── /app          → shortn-web   (React SPA on nginx, :80)
  └── /  and /{code} → shortn-api  (:8080)
        ├── Postgres   (StatefulSet)
        └── Redis
```

Cloudflare sits in front of the origin as a proxy (the DNS record is orange-clouded). It
terminates TLS with the free Universal SSL certificate, so no Advanced Certificate Manager
subscription is needed. Because every visitor connects to Cloudflare rather than the VM, it also
absorbs DDoS traffic and keeps the origin's public IP out of DNS.

The origin is a single Azure VM, 2 vCPU and 4 GB of RAM, running k3s (a single-binary Kubernetes
distribution). One node means one failure domain: losing the VM takes the whole system down, and
Postgres data with it. Postgres durability rests on the `pg_dump` backups documented in
[runbook.md](runbook.md) and the `shortn-postgres-backup` CronJob in the chart.

## Ingress routing

k3s ships Traefik as its default ingress controller, so the chart sets `ingress.className: traefik`
rather than nginx. The ingress (`deploy/k8s/shortn/templates/ingress.yaml`) defines two paths on
the host `shortn.ashfak.dev`:

- `/app` (Prefix) → the `shortn-web` service. This serves the React dashboard SPA from an nginx
  container. The rule is more specific than `/`, so `/app/*` wins.
- `/` (Prefix) → the `shortn-api` service on port 8080. This catch-all serves both `/api/*` and the
  `/{code}` short-link redirects.

Short links own the root, so a bare `/{code}` resolves through the API to a redirect. A Cloudflare
redirect rule sends bare `/` to `/app/` so a visitor landing on the apex hits the dashboard rather
than the API's root handler.

## Lean profile

Production runs the values file `deploy/k8s/shortn/values-azure.yaml`, a reduced profile sized for
the 4 GB node:

| Component             | State on Azure | Notes                                            |
| --------------------- | -------------- | ------------------------------------------------ |
| api                   | on             | `replicas: 1`, autoscaling off                   |
| web (dashboard)       | on             | `web.enabled: true`                              |
| Postgres              | on             | StatefulSet                                      |
| Redis                 | on             | cache + rate-limiter + worker-id lease           |
| Redpanda              | off            | `redpanda.enabled: false`                        |
| analytics consumer    | off            | `analytics.enabled: false`                       |
| observability stack   | off            | Prometheus/Grafana/Loki/Tempo not deployed here  |
| SealedSecret          | off            | no sealed-secrets controller; Secret created directly |

Redpanda and the async analytics path are gated off because they do not fit alongside Postgres,
Redis, the API, and the dashboard on this node. With them off, the API publishes no click events;
per-code analytics still reads what is in Postgres.

The API rate limit is tightened for the public node: 20 requests per second with a burst of 40,
per client (`api.rateLimit.rps: 20`, `api.rateLimit.burst: 40`). That is generous for real human
traffic and blunts scripted abuse.

## Security lock-down

Three controls separate the public deployment from the local stack.

**Admin gating on list and delete.** Listing all links and deleting a link are gated behind the
`X-Admin-Key` header. The check (`internal/http/middleware.go`) uses
`subtle.ConstantTimeCompare` and is fail-closed: if `ADMIN_KEY` is empty, the admin endpoints are
locked rather than open. A request without the correct header gets rejected. Create and per-code
analytics remain public (create is rate-limited).

**Rate-limit key from the rightmost `X-Forwarded-For` entry.** Exactly one trusted proxy sits
directly in front of the API — Cloudflare reaches the origin, Traefik fronts the pod, and the proxy
appends the connecting address to the end of `X-Forwarded-For`. The limiter keys on that rightmost
entry (`internal/http/ratelimit.go`), which is spoof-resistant: a client forging the header only
prepends entries, and the trusted proxy still appends the real address after them, so the last one
wins. `X-Real-IP` is deliberately not trusted, since Traefik neither sets nor strips it and a client
could forge it.

**Ops endpoints off the public port.** `/healthz`, `/readyz`, and `/metrics` listen on the internal
ops port 9090 (`OPS_PORT`, `internal/config/config.go`), a separate listener that is never routed
through the ingress. Public traffic reaches only the application port 8080. To read metrics you
port-forward to 9090 over the tunnel (`make azure-metrics`); they are not reachable from the
internet.

## Operating the live cluster

The cluster's Kubernetes API is not exposed publicly. All operations run from a laptop over an SSH
tunnel to the VM. The relevant targets live in the `# azure (live k3s deploy)` section of the
`Makefile`.

Open the tunnel first, in its own terminal, and leave it running:

```
make azure-tunnel    # ssh -L 6443:127.0.0.1:6443 -N azureuser@<vm-ip>
```

Every `azure-*` target below talks to the live cluster through that tunnel, using a dedicated
kubeconfig (`~/.kube/shortn-azure.yaml`):

| Target                              | What it does                                                          |
| ----------------------------------- | -------------------------------------------------------------------- |
| `make azure-kubeconfig`             | copy the cluster's kubeconfig from the VM to the laptop (run once)    |
| `make azure-secret ADMIN_KEY=...`   | create/update `shortn-api-secret` (DATABASE_URL, REDIS_URL, ADMIN_KEY) |
| `make azure-deploy`                 | `helm upgrade --install` the chart with `values-azure.yaml`          |
| `make azure-migrate`                | run DB migrations against live Postgres via a temporary port-forward  |
| `make azure-status`                 | pods, services, and ingress on the live cluster                       |
| `make azure-top`                    | live CPU/memory of the node and each pod (needs metrics-server)       |
| `make azure-logs`                   | tail the live API logs                                                |
| `make azure-admin-key`              | print the `ADMIN_KEY` currently stored in the Secret                  |
| `make azure-psql`                   | open a `psql` shell on the live Postgres                              |

`make azure-secret` refuses to run without `ADMIN_KEY` set; generate one with
`openssl rand -hex 32`. Secrets exist only as a Kubernetes Secret on the cluster — `DATABASE_URL`,
`REDIS_URL`, and `ADMIN_KEY` are never committed to git. The committed SealedSecret is disabled in
this profile because there is no sealed-secrets controller on the node; the Secret is created
directly via `azure-secret`.

### Shipping a new image

Images are built for `linux/amd64` (the Azure VM is x86, unlike the abandoned ARM target) and pushed
to GHCR, then pinned by tag in `values-azure.yaml`:

```
make azure-image       # build + push ghcr.io/ashfak-hossain/shortn-api:<short-sha>
make azure-web-image   # build + push ghcr.io/ashfak-hossain/shortn-web:<short-sha>
```

Set the printed tag in `values-azure.yaml` (`image.tag` for the API, `web.tag` for the dashboard),
then run `make azure-deploy`.

## Known limitation: edge-only TLS

TLS is currently terminated only at Cloudflare's edge. The Cloudflare-to-origin hop is not yet
Full (strict): there is no trusted certificate on the origin, so Cloudflare does not verify the
origin's identity end to end. The planned fix is an origin certificate issued by cert-manager,
deferred to the next host move.

## See also

- [runbook.md](runbook.md) — operational procedures, failure modes, backup/restore.
- [reference/services-and-ports.md](reference/services-and-ports.md) — services, ports, and how the pieces fit.
- [explanation/orchestration-and-delivery.md](explanation/orchestration-and-delivery.md) — the Kubernetes,
  Helm, ArgoCD, and Terraform foundations the chart builds on.
- [../SECURITY.md](../SECURITY.md) — the threat model behind the lock-down.
