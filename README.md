# shortn

A distributed URL shortener built as a practice project of distributed systems and DevOps concepts.

**Stack:** Go · PostgreSQL · Redis · Docker · GitHub Actions · React

## Status

A Postgres-backed URL shortener: `POST /api/links` returns a short code, `GET /{code}` 302-redirects to the original URL, data persists in PostgreSQL, and the whole stack runs with `docker compose up`. Clean layered architecture (`http` → domain → `store`), unit + integration (testcontainers) tests, green CI.
Next: **Phase 2 — caching the read path (Redis)**.

## Run

Requires Go 1.26+, Docker, and `make`.

### Full stack (API + Postgres) via Docker Compose

```sh
docker compose -f deploy/compose/docker-compose.yml up --build
```

This starts Postgres, applies migrations, and serves the API on `:8080`.

### API examples

```sh
# create a short link
curl -s -X POST localhost:8080/api/links -d '{"url":"https://example.com"}'
# → {"code":"Ab3xK9p","short_url":"http://localhost:8080/Ab3xK9p","long_url":"https://example.com"}

# follow it — 302 redirect to the original URL
curl -i localhost:8080/Ab3xK9p

# errors use a consistent shape: {"error":"..."}
curl -i localhost:8080/unknown                                    # 404
curl -i -X POST localhost:8080/api/links -d '{"url":"not-a-url"}' # 400
```

Health & readiness:

```sh
curl -i localhost:8080/healthz   # 200 — liveness (dependency-free)
curl -i localhost:8080/readyz    # 200 — readiness (checks the database)
```

### Local development

```sh
make run          # run the API (needs a reachable Postgres via DATABASE_URL)
make test         # go test ./...  (unit tests)
make lint         # go vet + golangci-lint
make migrate-up   # apply migrations
make migrate-down # roll back the last migration
make docker       # build the container image (< 30MB)

# integration tests spin a real Postgres via testcontainers (needs Docker):
go test -tags integration ./...
```

Config is read from the environment: `PORT`, `LOG_LEVEL`, `ENV`, `DATABASE_URL`.

## Architecture

Clean, layered design with dependency inversion:

- `internal/http` — chi handlers, validation, the consistent JSON error shape. Knows HTTP, not SQL.
- `internal/shortener` — core domain (create/resolve, URL rules, collision-retry). Defines the `LinkStore` and `IDGenerator` interfaces; imports no pgx and no `net/http`.
- `internal/store` — pgx Postgres repository implementing `LinkStore`. The only package with SQL.
- `internal/idgen` — `RandomBase62` implementing `IDGenerator` (Phase 3 swaps in a distributed scheme behind the same interface).

The domain depends on interfaces, not concrete I/O, so implementations swap without touching business logic. Decisions are recorded as [ADRs](docs/architecture/README.md).

## Performance

_Coming soon — benchmarks will be recorded in a later._
