# Infrastructure as Code with Terraform (0013)

**Status:** Accepted, 2026-06-19

## Context

[ADR 0011](0011-orchestration.md) runs `shortn` on a Kubernetes cluster and
[ADR 0012](0012-gitops-delivery.md) delivers to it via ArgoCD. Both assume a cluster exists,
with namespaces, and ArgoCD installed. If that bootstrap is a sequence of half-remembered shell
commands (`kind create cluster`, `kubectl create namespace`, `kubectl apply` the ArgoCD install),
it is not reproducible, not reviewable, and not auditable — the exact failure mode the rest of the
project has been engineered to avoid. The infrastructure itself is declared as code, even on
free-tier, to keep the same discipline.

Two decisions follow: which IaC tool, and what is in scope to declare given the
free-tier / local constraint.

## Decision

1. **Terraform** is the IaC tool. It is declarative (you describe desired infra; `plan` diffs it
   against a state file and the real world; `apply` reconciles, the same loop as k8s controllers
   and ArgoCD), tool-agnostic via providers, and the market standard.

2. **Scope is what can be declared for free, locally:** the `kind` cluster (kind provider),
   the namespaces (`shortn`, `argocd`) (kubernetes provider), and the ArgoCD install
   (helm provider). This is the day-0 bootstrap that everything else sits on.

3. **State is local and untracked.** `*.tfstate` is git-ignored, since it can contain secrets and
   is machine-specific. A real setup uses a remote backend (S3 + lock table, Terraform Cloud); the
   local file is a deliberate free-tier simplification.

4. **A real cloud cluster is out of scope here.** Standing up a managed cluster on a cloud provider
   belongs to the public deployment, not the bootstrap. The point is the discipline (declare →
   plan → apply → version → detect drift), not the size of the infrastructure.

## Alternatives considered

- **Pulumi** (IaC in a general-purpose language) — rejected: Terraform's declarative HCL and its
  `plan` dry-run are the more common industry baseline, and a real language is power this scope
  does not need.
- **Shell scripts / a Makefile target that runs `kind`/`kubectl`** — rejected: imperative, no
  drift detection, no `plan` preview, no state. It would work but is unreviewable as a diff.
- **Ansible** — rejected: configuration-management and imperative-leaning, aimed at provisioning
  hosts rather than declaring cloud/cluster resources; the wrong tool for standing up a cluster and
  installing ArgoCD.
- **Declaring the whole app (Deployments/Services) in Terraform too** — rejected: that is ArgoCD's
  job ([ADR 0012](0012-gitops-delivery.md)). Terraform builds the stadium (cluster, namespaces,
  ArgoCD); ArgoCD runs the games (the app, synced from git); Helm is the playbook. Keeping the
  boundary clean avoids two tools fighting over the same resources.
- **Remote state backend now** — rejected for free-tier/solo: it adds a cloud dependency for no
  benefit when one person runs `apply` on one laptop. It remains the production path.

## Consequences

**Easier:**

- The cluster bootstrap is reproducible byte-for-byte: `terraform destroy` then `apply` rebuilds
  the cluster + namespaces + ArgoCD from scratch.
- The bootstrap is reviewable as a PR diff and versioned with the code that runs on it.
- `terraform plan` is a dry-run read before acting, the safety property that hand-run commands
  lack, and re-running it surfaces drift.
- One reconciliation mental model spans the entire system: controllers, HPA, ArgoCD, and Terraform.

**Harder / what this now owes:**

- Another tool and its state model to operate; a corrupt or lost local state file means Terraform
  loses track of what it created (mitigated here because the resources are cheap and recreatable).
- A clear boundary discipline is required: Terraform stops at the cluster + ArgoCD; the app is
  ArgoCD's domain. Blurring it causes ownership conflicts.
- Local state is git-ignored and machine-bound, so this Terraform is not yet a team-shareable
  source of truth. Promoting it (remote backend, locking) is owed before any shared or real
  environment; a future ADR will record that when real cloud infra is introduced.

This record supersedes nothing. It bootstraps the cluster assumed by
[ADR 0011](0011-orchestration.md) and the ArgoCD agent of [ADR 0012](0012-gitops-delivery.md).
