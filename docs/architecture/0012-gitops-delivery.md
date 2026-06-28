# Continuous delivery: GitOps with ArgoCD, images on GHCR (0012)

**Status:** Accepted, 2026-06-19

## Context

[ADR 0011](0011-orchestration.md) puts `shortn` on Kubernetes, packaged as a Helm chart. That
answers *what runs*; it does not answer *how a new version gets there*. A push to `master`
automatically builds an image and deploys it, with a zero-downtime rolling update, and the system
is reproducible from git.

CI lints, tests, and builds. Two decisions remain: (1) where built images live, and (2) how an
image becomes a running deployment — pushed to the cluster by CI, or pulled into the cluster by an
in-cluster agent watching git (GitOps).

## Decision

1. **Images: GitHub Container Registry (GHCR).** A `push-image` CI job builds multi-arch
   (`linux/amd64,linux/arm64`) images for both `api` and `analytics` via `docker buildx`, tags them
   by git SHA (immutable, traceable), and pushes them using the built-in `GITHUB_TOKEN`. The
   `Dockerfile` (`ARG SERVICE`) builds both binaries, so there is one Dockerfile, not two. This
   extends the `lint`/`test`/`build` trio rather than restructuring it.

2. **Delivery: pull-based GitOps with ArgoCD.** ArgoCD runs in-cluster and continuously
   reconciles the cluster to a git source of truth (the Helm chart + values). A deploy is a git
   commit (CI bumps the image tag in `values`), which ArgoCD detects and syncs. It is never
   `kubectl apply` run from CI against the cluster.

3. **A single ArgoCD `Application`** points at the chart path in this repo, with automated sync.
   The "app-of-apps" pattern (one root Application managing many) is the scale-up answer and is
   deliberately not built.

4. **Zero-downtime is a property of the rolling update**, not of ArgoCD: ArgoCD triggers the
   Deployment change; Kubernetes does the roll, gated by the readiness probe (`/readyz`) and the
   graceful `SIGTERM` drain. Load-testing through a merge demonstrates it.

## Alternatives considered

- **Push-based CD: `kubectl apply` / `helm upgrade` from a GitHub Actions job.** Simpler, fewer
  moving parts. Rejected: it requires **cluster-admin credentials stored in CI** (a large blast
  radius and the credentials *leave* the cluster), it leaves no continuous record of desired
  state, and it can't detect or heal **drift** (a manual `kubectl edit` goes unnoticed). GitOps
  inverts the trust arrow: the cluster pulls; CI never touches it.
- **Flux instead of ArgoCD** — equally valid GitOps. Rejected on UI: ArgoCD's dashboard makes
  sync/health/drift visible at a glance. The principles transfer directly.
- **Docker Hub instead of GHCR** — rejected: GHCR is free for public repos, lives beside the
  source, and authenticates with the repo's own `GITHUB_TOKEN` (no extra secret to manage).
- **`latest` / branch tags for images** — rejected: mutable tags make "what's actually running?"
  unanswerable and break rollbacks. SHA tags are immutable and traceable to a commit.
- **Single-arch (amd64) images** — rejected: the dev machine is Apple Silicon (arm64) and CI/cloud
  is amd64; multi-arch via buildx makes one tag run everywhere.

## Consequences

**Easier:**

- **Git is the audit log and the rollback button**: every deploy is a commit, `git revert` is a
  rollback, and the repo always states what *should* be running.
- **No cluster credentials in CI** — the cluster pulls, shrinking the attack surface.
- **Drift detection and self-heal**: ArgoCD flags (or reverts) any out-of-band change, so the
  cluster cannot silently diverge from git.
- The reconciliation model is now consistent end-to-end (controllers, HPA, ArgoCD, Terraform all
  "diff desired vs actual"), which makes the whole system explainable with one mental model.

**Harder / what we now owe:**

- A new in-cluster component (ArgoCD) to install and understand; its bootstrap is itself declared
  in Terraform ([ADR 0013](0013-infrastructure-as-code.md)) so it isn't a click-ops island.
- The deploy flow has more hops (merge → CI build/push → tag-bump commit → ArgoCD sync → roll),
  so "why isn't my change live?" can fail at more places — mitigated by ArgoCD's UI surfacing sync
  status.
- The tag-bump step couples CI to the values file. It is kept simple; an image updater (e.g.
  ArgoCD Image Updater) would automate it.
- Public GHCR images mean the artifacts are world-readable — fine for this open-source repo;
  anything proprietary would need private packages.

This record supersedes nothing. It builds on [ADR 0011](0011-orchestration.md) (what runs) and is
bootstrapped by [ADR 0013](0013-infrastructure-as-code.md) (the cluster + ArgoCD install as code).
