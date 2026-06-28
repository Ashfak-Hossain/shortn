# Architecture Decision Records (ADRs)

This directory holds the Architecture Decision Records for `shortn`. An ADR is a
short, immutable note that captures one significant technical decision: the forces
behind it, what was chosen, what was rejected, and what living with the choice
costs. It is a commit message for an architectural change rather than a code
change.

See also: [CONTRIBUTING.md](../../CONTRIBUTING.md) for repo guidance.

An ADR is immutable. A changed decision is not an edit to an Accepted ADR; it is a
new ADR that supersedes it, with the old record's status flipped to
`Superseded by NNNN`. The history of why the system is the way it is stays intact.

## The template

Every ADR uses the same four-part skeleton. Records run to roughly one page.

```markdown
# <Short imperative title> (NNNN)

**Status:** Accepted, 2026-06-06

## Context

What forces are at play? What problem, constraint, or requirement makes this a
decision that has to be made? (No solution yet — just the situation.)

## Decision

What was chosen, stated plainly. One or two sentences.

## Alternatives considered

- **Option A** — why rejected.
- **Option B** — why rejected.

## Consequences

What becomes easier (good) and what becomes harder or what is now owed (bad /
trade-off). State the downsides; a one-sided ADR is a red flag.
```

Status values: `Proposed` → `Accepted` → (later) `Superseded by NNNN` or
`Deprecated`. An Accepted record is the current decision; a Superseded record is
kept for history and points at the ADR that replaced it.

## Numbering & naming convention

Files are named `NNNN-kebab-case-title.md`:

- `NNNN` — a zero-padded, monotonically increasing integer (`0001`, `0002`, …).
  Numbers are never reused, even if an ADR is superseded.
- `kebab-case-title` — a short slug, e.g. `language-and-runtime`.

The number is an identity, not a priority. ADRs are cited by number ("see ADR
0003") the way RFCs are.

## Index

| #    | Title                                                                      | Status   | Date       |
| ---- | -------------------------------------------------------------------------- | -------- | ---------- |
| 0001 | [Language and runtime](0001-language-and-runtime.md)                       | Accepted | 2026-06-06 |
| 0002 | [Config from environment](0002-config-from-environment.md)                 | Accepted | 2026-06-06 |
| 0003 | [Container strategy](0003-container-strategy.md)                           | Accepted | 2026-06-06 |
| 0004 | [ID generation strategy](0004-id-generation-strategy.md)                   | Accepted | 2026-06-11 |
| 0005 | [URL normalization](0005-url-normalization.md)                             | Accepted | 2026-06-11 |
| 0006 | [Caching strategy](0006-caching-strategy.md)                               | Accepted | 2026-06-12 |
| 0007 | [Distributed ID generation](0007-distributed-id-generation.md)             | Accepted | 2026-06-13 |
| 0008 | [Messaging & delivery semantics](0008-messaging-and-delivery-semantics.md) | Accepted | 2026-06-14 |
| 0009 | [Resilience & reliability](0009-resilience.md)                             | Accepted | 2026-06-16 |
| 0010 | [Observability stack & telemetry export](0010-observability.md)            | Accepted | 2026-06-16 |
| 0011 | [Orchestration: Kubernetes on kind, Helm packaging](0011-orchestration.md) | Accepted | 2026-06-19 |
| 0012 | [Continuous delivery: GitOps with ArgoCD, GHCR](0012-gitops-delivery.md)   | Accepted | 2026-06-19 |
| 0013 | [Infrastructure as Code with Terraform](0013-infrastructure-as-code.md)    | Accepted | 2026-06-19 |
| 0014 | [Worker ID assignment via Redis lease](0014-worker-id-assignment.md)       | Accepted | 2026-06-24 |
