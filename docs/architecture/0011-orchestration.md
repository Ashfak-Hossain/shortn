# Orchestration: Kubernetes, packaged with Helm (0011)

**Status:** Accepted, 2026-06-19

## Context

`shortn` could run as a single `docker compose up`: API replicas, an analytics consumer, Postgres,
Redis, Redpanda, and the observability stack, all on one host, started in a fixed order. Compose
cannot restart a workload whose host died, move workloads off a failed machine, autoscale, or roll
out a new version without a gap. The system runs every service under a container orchestrator that
does those jobs continuously, on free-tier / self-hosted infrastructure.

Two decisions are forced here: which orchestrator, and how to package the many near-identical
manifests so one definition serves dev and prod without copy-paste. Delivery — how new versions
reach the cluster — is a separate decision; see [ADR 0012](0012-gitops-delivery.md). Declaring the
cluster itself is [ADR 0013](0013-infrastructure-as-code.md).

## Decision

1. **Kubernetes** is the orchestrator. It is the industry standard, and its core objects map
   cleanly onto what the system already has: Deployments back the stateless API and analytics
   consumer, a Service fronts the nginx load balancer, probes wire to the `/healthz` and `/readyz`
   split, and graceful shutdown wires to the `SIGTERM` handler.

2. **Local cluster: `kind`** (Kubernetes-in-Docker) for development only. Each node is a Docker
   container on the laptop — a real, conformant cluster at zero cost. The live system runs on k3s
   (covered in the deployment docs); `kind` exists to develop and test the manifests locally.

3. **Packaging: Helm**, one umbrella chart for `shortn`'s own services with `values.yaml` +
   `values-dev.yaml` + `values-prod.yaml`. Raw manifests came first for the API, then templating;
   the chart was not Helm-first.

4. **Stateful vs stateless is a hard line.** API and analytics are stateless: `Deployment` +
   `HorizontalPodAutoscaler`, scaled freely. Postgres is stateful: one instance (`StatefulSet` +
   `PersistentVolumeClaim`), not an in-cluster HA cluster. Redis (fail-open cache) and Redpanda
   (single dev broker) run as single Deployments.

5. **Secrets** live in Kubernetes `Secret` objects, injected as env, so no application code
   changes. Plaintext secrets are not committed. A `Secret` is only base64-encoded; the production
   path (Sealed Secrets / External Secrets Operator / managed secret store) is out of scope for
   free-tier — see Consequences.

## Alternatives considered

- **`k3d` / k3s, or `minikube`, for the local cluster** — both fine and lighter. Rejected in favor
  of `kind` because `kind` is the CNCF conformance tool, runs upstream kubeadm Kubernetes (closest
  to what a managed cloud cluster runs), and is what CI mirrors. The concepts are identical across
  all three; this is a low-stakes pick standardized for consistency.
- **Docker Swarm / Nomad** — simpler orchestrators. Rejected: Swarm is effectively in maintenance,
  and the goal is the industry-standard skill that the market asks for.
- **Raw manifests only, no Helm** — fewer concepts. Rejected: by the time every service has a
  Deployment/Service/ConfigMap/Secret in two environments, hand-maintained YAML duplicates and
  drifts. Helm's chart-as-a-function (values = args, manifests = return value) is what ArgoCD syncs.
- **Kustomize overlays instead of Helm** — also valid, no templating language. Rejected: Helm's
  packaging, release history (`helm rollback`), and dependency model fit the multi-service shape
  better. A reasonable alternative.
- **Running a real Postgres HA cluster in k8s** (operator, replication, failover) — rejected as
  over-engineering at free-tier traffic. The system runs one Postgres; the real-world answer is a
  managed database outside the cluster.

## Consequences

**Easier:**

- Self-healing, autoscaling, and zero-downtime rolling updates are declarative properties, not
  scripts; the existing health probes and graceful shutdown plug straight in.
- Being 12-factor and stateless pays off: config flows via ConfigMap/Secret and the API scales
  horizontally with no application code change.
- One Helm chart + per-environment values replaces dozens of hand-edited manifests; `helm template`
  makes the rendered output reviewable before it ships.

**Harder / what is now owed:**

- A large surface area of objects and failure modes (`ImagePullBackOff`, evicted pods, PVC binding,
  probe misconfiguration), managed by building incrementally (raw → Helm → autoscale).
- Resource requests/limits are mandatory: the scheduler and the HPA both depend on requests;
  omitting them silently breaks autoscaling and risks eviction.
- Local-image gotcha: images must be `kind load`ed (or pulled from GHCR) into the cluster;
  laptop-built images aren't visible to the cluster runtime by default.
- Secrets are base64, not encrypted, and sit in etcd. Acceptable for a free-tier cluster only
  because nothing real is exposed; a Sealed/External Secrets story is owed before any genuinely
  sensitive deployment, and a future ADR will record it.
- Single Postgres is a single point of failure by deliberate choice; the mitigation (managed DB /
  HA operator) is deferred, not forgotten.

This record supersedes nothing. Delivery is [ADR 0012](0012-gitops-delivery.md); declaring the
cluster as code is [ADR 0013](0013-infrastructure-as-code.md).
