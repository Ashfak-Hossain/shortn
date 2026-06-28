# Contributing

This guide covers how to build, test, and extend `shortn`, and the conventions the
codebase holds to. Start with [ARCHITECTURE.md](ARCHITECTURE.md) for the design, then read
the [Architecture Decision Records](docs/architecture/README.md) for why each choice was made.

## Prerequisites

- Go 1.26+
- Docker (for the local stack and the integration tests)
- `golangci-lint` (installed as a binary so its version is reproducible: `brew install golangci-lint`)
- `make`

## Getting started

```sh
# Bring up the full local stack (Postgres, Redis, Redpanda, migrations, API behind nginx).
docker compose -f deploy/compose/docker-compose.yml up --build

# Or run the API directly against a reachable Postgres/Redis.
make run
```

Configuration is read from the environment; see [`.env.example`](.env.example) for every
variable and its default.

## Build, test, lint

```sh
make test                      # unit tests (go test ./...)
go test -tags integration ./... # integration tests — spin real Postgres/Redis via testcontainers (needs Docker)
make lint                      # go vet + golangci-lint
make docker                    # build the container image
```

CI runs the same `lint`, `test`, and `build` gates on every push and pull request. A change
is not done until they pass.

Tests are table-driven: each case is a `{name, input, want}` struct run as a named subtest
with `t.Run`. HTTP handlers are tested with `httptest` rather than a live server.

## The architectural rule to preserve

The domain depends on interfaces, not concrete I/O. The one invariant that keeps it that way:

> **`internal/shortener` imports no `database/sql`, no `pgx`, and no `net/http`.**

It defines the interfaces it needs (`LinkStore`, `IDGenerator`) and receives implementations
through its constructor. SQL lives only in `internal/store`. The HTTP layer lives only in
`internal/http`. New persistence, caching, or transport code is added as an implementation of
an existing interface and wired in at the composition root (`cmd/api/main.go`) — not by
reaching into the domain. See [the clean-architecture explainer](docs/explanation/clean-architecture-and-di.md).

## Conventions

- **Decisions get an ADR.** Any time you choose one approach over another, add a record in
  [docs/architecture/](docs/architecture/README.md) as Context → Decision → Alternatives →
  Consequences. ADRs are immutable: a reversed decision is a new ADR that supersedes the old
  one, not an edit.
- **Conventional commits**, small and focused: `feat:`, `fix:`, `docs:`, `chore:`, `build:`,
  `ci:`, `test:`. One logical change per commit so diffs stay reviewable.
- **One branch per change**, merged to `master` via a pull request that says what changed and
  why.
- **Doc comments** follow [docs/standards/go-doc-comments.md](docs/standards/go-doc-comments.md).
- **Schema changes** are paired `*.up.sql` / `*.down.sql` files in `migrations/` (golang-migrate);
  the down file must actually reverse the up file.
- **Config** is read once in `internal/config`. Adding a setting is one field on the struct
  and one line in `Load()`; document it in `.env.example`.
