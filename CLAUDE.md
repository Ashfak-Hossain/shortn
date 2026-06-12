# CLAUDE.md

This file guides Claude Code (claude.ai/code) at the start of every session in this repo. Read it first; the rules in **Working agreement** override default behavior.

## Project

`shortn` is a URL shortener, but the URL shortener is the excuse — the real goal is a guided, hands-on tour of distributed-systems and DevOps concepts (clean architecture, containers, caching, queues, observability, Kubernetes, IaC) built strictly phase by phase. Each phase adds one production concern to a service that must keep working at every step. The full 12-week curriculum, including the Definition of Done and "Defend" questions for each phase, lives in [PLAN.md](PLAN.md); the per-phase worklists live in [docs/phases/README.md](docs/phases/README.md). Do not jump ahead of the current phase.

## Current status

**Phase 1 — Core URL shortener (MVP).** Goal: `POST /api/links` returns a short code; `GET /{code}` redirects to the original URL; data persists in PostgreSQL; runs locally via `docker-compose up`. The worklist source of truth is [PLAN.md](PLAN.md) Phase 1 — create `docs/phases/phase-1.md` from [docs/phases/\_TEMPLATE.md](docs/phases/_TEMPLATE.md) when the phase starts.

Phase 0 (environment, Go HTTP spine, lint/test/build CI) is **complete and green** — see [docs/phases/phase-0.md](docs/phases/phase-0.md). Module path is `github.com/Ashfak-Hossain/shortn` (matches the real GitHub username at github.com/Ashfak-Hossain). Once code imports it, renaming it is a painful sweep, so never rename it later.

## Tech stack

Stack is **locked** (see ADRs in [docs/architecture/](docs/architecture/README.md)). Phase column = when it first appears.

| Concern                 | Choice                                          | Phase |
| ----------------------- | ----------------------------------------------- | ----- |
| Language/runtime        | Go                                              | P0    |
| HTTP router             | chi v5 (`github.com/go-chi/chi/v5`)             | P0    |
| Logging                 | `log/slog` (stdlib), JSON handler               | P0    |
| Config                  | stdlib `os.Getenv` + `Config` struct + `Load()` | P0    |
| Container               | multi-stage build → `gcr.io/distroless/static`  | P0    |
| CI                      | GitHub Actions (`lint`/`test`/`build` jobs)     | P0    |
| DB driver               | pgx (`github.com/jackc/pgx/v5`)                 | P1    |
| Database                | PostgreSQL                                      | P1    |
| Migrations              | golang-migrate                                  | P1    |
| Cache                   | Redis                                           | P2    |
| Reverse proxy / scaling | nginx                                           | P3    |
| Message queue           | NATS                                            | P4    |
| Observability           | Prometheus, Grafana, Loki, Tempo (OTel)         | P6    |
| Orchestration           | Kubernetes, Helm, ArgoCD                        | P7    |
| Infra-as-code           | Terraform                                       | P7    |
| Frontend                | React dashboard                                 | P9    |

## Architecture

Clean, layered design with **dependency inversion**: the domain depends on interfaces, not on concrete I/O. Each package has one job and a strict allowed-dependency direction (`http` → `shortener` → interfaces; never the reverse).

- `internal/http` — chi router, handlers, middleware. Knows HTTP; knows nothing about SQL.
- `internal/shortener` — core domain logic (shorten/resolve rules). **No `database/sql`, no `net/http` imports.** Defines the interfaces it needs (e.g. `LinkStore`, `IDGenerator`).
- `internal/store` — pgx repository implementing `LinkStore`. **The only place SQL lives.**
- `internal/idgen` — ID generation, implements `IDGenerator` (P3 swaps in a distributed scheme).
- `internal/cache` — Redis layer (P2).
- `internal/events` — queue publish/consume (P4).
- `internal/config` — `Config` struct + `Load()` from env.

**Why interfaces:** the domain declares `LinkStore`/`IDGenerator` as interfaces and receives implementations via constructor injection. That lets us swap a Postgres store for a fake in tests, or swap a counter ID generator for a Snowflake-style one in Phase 3, **without touching `shortener` or `http`**. This is the payoff we keep defending: the cost of change stays flat as the system grows.

## Directory layout

```
shortn/
├── PLAN.md                  # curriculum (source of truth)
├── README.md                # grows every phase
├── docs/
│   ├── architecture/        # diagrams, ADRs (decision records)
│   └── runbook.md
├── cmd/
│   ├── api/                 # API service entrypoint
│   └── analytics/           # analytics consumer entrypoint (added Phase 4)
├── internal/
│   ├── shortener/           # core domain logic
│   ├── store/               # postgres repository
│   ├── cache/               # redis layer (Phase 2)
│   ├── idgen/               # id generation (Phase 3)
│   ├── events/              # queue publish/consume (Phase 4)
│   └── http/                # handlers, middleware, router
├── migrations/              # SQL migrations
├── deploy/
│   ├── compose/             # docker-compose files
│   ├── k8s/                 # raw manifests / Helm chart (Phase 7)
│   └── terraform/           # IaC (Phase 7)
├── load/                    # k6 scripts (Phase 8)
├── web/                     # React dashboard (Phase 9)
└── .github/workflows/       # CI/CD (Phase 0 onward)
```

## Commands

These are the **intended** make targets — they exist once the Makefile, Go module, and source are written during the Phase 0 build (the user writes that code by hand; nothing here runs against an empty repo yet).

- `make run` — run the API locally.
- `make test` — `go test ./...`.
- `make lint` — `go vet` + `golangci-lint run`.
- `make docker` — build the container image (target: final image < 30MB).

More targets are added per phase (`migrate-up`/`migrate-down` P1, `scale` P3, `run-analytics` P4, `chaos` P5, `kind`/`helm` P7).

## Working agreement (CRITICAL — overrides default behavior)

**Golden rule: never write code the user does not understand.** Assume that after any non-trivial generation the user will ask _"explain this line by line, and what breaks if it weren't here."_ If you can't defend a line, don't write it.

- **The user writes the application code by hand.** Claude's role is plan / why / how / shell commands / docs / review — **not** auto-writing app code. Provide design, structure, signatures, small illustrative snippets, and exact runnable commands; let the user type the implementation.
- **One task at a time.** Small, reviewable diffs. No batching unrelated changes.
- **Outline before code.** Before any implementation, describe the approach and get approval.
- **Always explain design choices and tradeoffs** — name the alternative and why it lost.
- **No panics for normal errors; config from env; graceful shutdown from day one** (Kubernetes sends SIGTERM in Phase 7).

## Conventions

- **Branching:** one branch per phase (`phase-0-setup`, `phase-1-core`, …), merged to `master` via PR with a description.
- **Commits:** conventional and small (`feat:`, `fix:`, `docs:`, `chore:`, `build:`, `ci:`, `test:`).
- **ADRs:** every time we pick X over Y, write a one-page record in [docs/architecture/](docs/architecture/README.md) as _Context → Decision → Alternatives → Consequences_. Phase 0 ships ADRs 0001–0003.
- **Definition of Done is non-negotiable.** A phase is done only when its PLAN.md checklist passes **and** the Defend questions can be answered. "It runs on my machine" is not done.
-

## Go Doc Comments

- Doc comment rules: see docs/standards/go-doc-comments.md

## Docs map

- [docs/infrastructure.md](docs/infrastructure.md) — services, ports, how the pieces fit.
- [docs/phases/README.md](docs/phases/README.md) — phase index; [phase-0.md](docs/phases/phase-0.md) is the active worklist; [\_TEMPLATE.md](docs/phases/_TEMPLATE.md).
- [docs/architecture/README.md](docs/architecture/README.md) — ADR index ([0001](docs/architecture/0001-language-and-runtime.md), [0002](docs/architecture/0002-config-from-environment.md), [0003](docs/architecture/0003-container-strategy.md)).
- [docs/runbook.md](docs/runbook.md) — operational procedures.
