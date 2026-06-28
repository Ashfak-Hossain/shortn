# Orchestration and delivery

How `shortn` runs on Kubernetes, how a new image reaches the cluster, and how the
cluster itself is created. This doc explains the mechanisms and the shape of the
system; the decisions and the alternatives that were weighed live in
[ADR 0011](../architecture/0011-orchestration.md) (orchestration),
[ADR 0012](../architecture/0012-gitops-delivery.md) (GitOps delivery), and
[ADR 0013](../architecture/0013-infrastructure-as-code.md) (IaC). Read those for "why
this and not that"; read this for "how it fits together."

Two clusters are in play. Local development runs on **kind** (Kubernetes-in-Docker,
each node a Docker container on the laptop), which is what the Helm chart and
Terraform target by default. The live public system runs on a **single-node k3s** VM
on Azure with a leaner profile; that deployment and its profile are described in
[deployment.md](../deployment.md).

## Declarative reconciliation

Kubernetes takes desired state, not commands. The chart declares "three healthy
replicas of this image, reachable on 8080"; background control loops (controllers)
compare that target to what is actually running and act to close the gap. Declaring
three replicas and losing a pod takes the count to 2 ≠ 3, so the controller starts a
replacement. Nothing issued a "restart" — the target was fixed and reality was
reconciled toward it.

The same loop recurs at every layer in this system: the Deployment controller holding
a replica count, the HPA holding a CPU target, ArgoCD holding the cluster equal to
git, and Terraform holding infrastructure equal to its `.tf` files. The dependency on
clean health signals is why the readiness and liveness probes are split and the service
shuts down gracefully: reconciliation can only act correctly if it can tell a healthy pod from a
hung one and can drain a pod cleanly before replacing it.

## The objects shortn uses

Each object is a declarative record (`kind`, `metadata`, `spec`, `status`) submitted to
the API server and reconciled by a controller. The chart under
`deploy/k8s/shortn/templates/` defines the following set.

**Deployment** — the stateless workloads: the API (`api-deployment.yaml`), the
analytics consumer (`analytics-deployment.yaml`), the React dashboard
(`web-deployment.yaml`), and Redis (`redis-deployment.yaml`). A Deployment declares an
image, a replica count, env sources, probes, and resource requests/limits, and manages
a ReplicaSet that holds the pod count. Changing the image tag triggers a rolling
update. The API and analytics consumer hold no per-request state in memory, so any pod
is interchangeable with any other — that is what lets the replica count move freely.

**Service** — a stable virtual IP and in-cluster DNS name load-balancing across pods
that match a label selector, tracking pod churn automatically. Every workload has one
(`api-service.yaml`, `postgres-service.yaml`, `redis-service.yaml`, etc.), all
ClusterIP. The API Service fronts port 8080 (public traffic and redirects); the ops
port 9090 (health, readiness, metrics) is exposed on the pod but deliberately not
routed by the ingress.

**Ingress** (`ingress.yaml`) — HTTP routing from outside the cluster by host and path.
A single host (`.Values.ingress.host`) carries two rules: `/app` (more specific, so it
wins) routes to the `shortn-web` dashboard when `web.enabled` is set, and a catch-all
`/` routes to `shortn-api`, which serves both `/api/*` and the `/{code}` short-link
redirects. The `ingressClassName` is templated — `nginx` on kind, `traefik` on k3s,
which ships Traefik by default. The rules are inert until an ingress controller pod
executes them.

**ConfigMap and Secret** — `api-configmap.yaml` holds non-sensitive config
(`LOG_LEVEL`, broker addresses, OTel endpoint); the secret holds `DATABASE_URL` and
`REDIS_URL`. Both are injected with `envFrom` (`configMapRef` + `secretRef`). Because
`internal/config` reads everything from the environment, moving from a compose
`environment:` block to a ConfigMap/Secret required no application code change.

**SealedSecret** (`sealedsecret.yaml`) — a plain Kubernetes Secret is only
base64-encoded in etcd, so committing one commits a plaintext password. The chart
instead commits a Bitnami SealedSecret: the `DATABASE_URL` and `REDIS_URL` values are
asymmetrically encrypted to a key only the in-cluster sealed-secrets controller holds,
which decrypts them into a real Secret at runtime. This is gated behind
`sealedSecret.enabled`. On a cluster without the controller (the k3s VM), it is turned
off and the Secret is created directly.

**StatefulSet + PVC for Postgres** (`postgres-statefulset.yaml`) — Postgres owns the
data and cannot be replicated by simply load-balancing writes across copies, so it runs
as a single StatefulSet with a `volumeClaimTemplates` PVC (`1Gi`, `ReadWriteOnce`). The
PVC binds storage that survives pod restarts; the container filesystem does not.
Redpanda runs the same way (`redpanda-statefulset.yaml`) when enabled. The rule the
topology encodes: stateless workloads scale out as identical pods; the stateful piece
is kept to one instance (or, in a larger build, outsourced to a managed database).

**HorizontalPodAutoscaler** (`api-hpa.yaml`) — rendered only when
`api.autoscaling.enabled` is true. It targets the API Deployment and holds average CPU
near `targetCPUUtilizationPercentage` (50% by default) between `minReplicas` and
`maxReplicas` (1 to 4). The percentage is measured against each pod's CPU *request*, so
the HPA cannot compute utilization without `resources.requests` being set — which they
are, at `50m`/`64Mi` requested, `250m`/`128Mi` limit. When the HPA owns the replica
count, the Deployment template omits its own `replicas` field to avoid the two fighting.

A backup CronJob (`postgres-backup-cronjob.yaml`) runs `pg_dump` on a schedule; its
restore procedure is in the runbook.

## Probes and rolling updates

The kubelet on each node runs the probes. Liveness hits `/healthz` (dependency-free); on
failure the container is restarted. Readiness hits `/readyz` (which checks Postgres); on
failure the pod is removed from its Service's endpoints but not restarted, then re-added
when the dependency recovers. Both target the ops port 9090. Liveness is kept
dependency-free on purpose: if it checked Postgres, a brief Postgres outage would fail
every pod's liveness at once and the kubelet would restart the whole fleet, turning a
dependency blip into a self-inflicted outage.

A rolling update shifts replicas from the old ReplicaSet to a new one a few at a time. A
new pod receives traffic only after `/readyz` passes, and an old pod leaves the Service
before it is killed; the API's `SIGTERM` handler then drains in-flight requests. Those
two gates — readiness before traffic, graceful shutdown before exit — are what make a
deploy drop zero requests. `kubectl rollout undo` reverts to the prior ReplicaSet, which
is still described in the cluster.

## The Helm chart

The chart lives at `deploy/k8s/shortn`: a `Chart.yaml` (chart version `0.1.0`),
`templates/` of parameterized manifests, and four values files. A chart behaves like a
function — `values` are the arguments, the rendered manifests are the return value, and
`helm template` prints that return value without applying it.

- `values.yaml` — defaults. Analytics and Redpanda on, the dashboard off, the
  SealedSecret on, autoscaling off, ingress class `nginx`, host `shortn.localhost`. The
  API rate limit is set high (1000 rps / 2000 burst) so k6 load tests aren't throttled.
- `values-dev.yaml` / `values-prod.yaml` — kind overrides; dev enables autoscaling,
  prod uses fixed replicas.
- `values-azure.yaml` — the live lean profile: images from `ghcr.io/ashfak-hossain/`
  with a pinned tag, the dashboard on, analytics and Redpanda off (memory), the
  SealedSecret off (no controller on that cluster), ingress class `traefik`, host
  `shortn.ashfak.dev`, and the rate limit dropped to 20 rps / 40 burst for a public
  single node.

Several pieces are opt-in by design so that a small cluster can run the core shortener
without them: `web.enabled`, `analytics.enabled`, `redpanda.enabled`,
`sealedSecret.enabled`, and `api.autoscaling.enabled`.

## GitOps delivery with ArgoCD

Delivery is pull-based. CI builds and pushes a versioned image to GHCR; it never holds
cluster credentials. ArgoCD runs *inside* the cluster, watches git, and reconciles the
cluster to match — the same desired-vs-actual loop, now with git as the desired state.
A deploy is a commit, not a `kubectl apply` from a laptop or CI.

The wiring is `deploy/k8s/argocd/shortn-application.yaml`, an ArgoCD `Application` in the
`argocd` namespace. It points at this repo (`master` branch), the chart path
`deploy/k8s/shortn`, and the `values-dev.yaml` values file. Its sync policy is automated
with `selfHeal: true` (manual drift is reverted to match git), `prune: true` (resources
removed from git are deleted), and `CreateNamespace=true`. The end-to-end path: merge to
`master` → CI builds and pushes `ghcr.io/.../shortn-api:<sha>` → a commit bumps the image
tag in values → ArgoCD detects the git change → ArgoCD syncs the chart → rolling update.

## Terraform: cluster, namespaces, ArgoCD install

Everything above describes what runs *in* a cluster. Terraform creates the cluster and
bootstraps ArgoCD, so that day-0 setup is versioned and reproducible rather than a series
of half-remembered commands. It is declarative with reconciliation too: `terraform plan`
diffs the `.tf` files against a state file and the real world and shows exactly what will
change before `terraform apply` makes it so.

`deploy/terraform/main.tf` uses three providers — `tehcyx/kind`, `hashicorp/helm`, and
`hashicorp/kubernetes`. It declares a `kind_cluster` resource (a control-plane node
labeled `ingress-ready=true`, host ports 80/443 mapped so the ingress controller is
reachable), points the helm and kubernetes providers at that cluster using the
credentials the kind resource exports (no kubeconfig file), and installs ArgoCD as a
`helm_release` from the `argo-helm` repository, pinned to chart version `7.7.11` for
reproducibility. The application's own manifests are *not* applied by Terraform — that is
ArgoCD's job, watching git. Terraform builds the cluster; ArgoCD keeps the app synced to
git; Helm packages the manifests. The k3s VM is provisioned outside this Terraform scope;
see [deployment.md](../deployment.md).

## Scope and honest limits

- **No multi-node production cluster.** Local work is single-node kind; the live system
  is single-node k3s. The orchestration concepts are identical at one node or many.
- **No Postgres HA.** One StatefulSet instance with a PVC. Real high availability
  (replication, failover) is a hard, separate problem, isolated here or outsourced to a
  managed database. The backup CronJob plus a proven restore is the durability story.
- **No service mesh, no Kustomize overlays.** Per-environment values files cover the
  variation the system actually has.
