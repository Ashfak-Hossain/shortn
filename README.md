# shortn

A distributed URL shortener built as a practice project of distributed systems and DevOps concepts.

**Stack:** Go · PostgreSQL · Redis · Docker · GitHub Actions · React

## Status

A Postgres-backed URL shortener with a Redis read-through cache: `POST /api/links` returns a short code, `GET /{code}` 302-redirects to the original URL (served from Redis on a hit, Postgres on a miss), data persists in PostgreSQL, and the whole stack runs with `docker compose up`. Clean layered architecture (`http` → domain → `store`), with the cache wired in as a `LinkStore` decorator so the domain never learns Redis exists; cache failures fail open (degrade to Postgres), and concurrent misses are collapsed with `singleflight`. Unit + integration (testcontainers) tests, green CI.
Next: **Phase 3 — distributed IDs & horizontal scaling**.

## Run

Requires Go 1.26+, Docker, and `make`.

### Full stack (API + Postgres + Redis) via Docker Compose

```sh
docker compose -f deploy/compose/docker-compose.yml up --build
```

This starts Postgres and Redis, applies migrations, and serves the API on `:8080`.

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

Config is read from the environment: `PORT`, `LOG_LEVEL`, `ENV`, `DATABASE_URL`, `REDIS_URL`.

## Architecture

Clean, layered design with dependency inversion:

- `internal/http` — chi handlers, validation, the consistent JSON error shape. Knows HTTP, not SQL.
- `internal/shortener` — core domain (create/resolve, URL rules, collision-retry). Defines the `LinkStore` and `IDGenerator` interfaces; imports no pgx and no `net/http`.
- `internal/store` — pgx Postgres repository implementing `LinkStore`. The only package with SQL.
- `internal/idgen` — `RandomBase62` implementing `IDGenerator` (Phase 3 swaps in a distributed scheme behind the same interface).

The domain depends on interfaces, not concrete I/O, so implementations swap without touching business logic. Decisions are recorded as [ADRs](docs/architecture/README.md).

## Performance

Redis read-through cache (cache-aside) in front of Postgres. To show the
effect, the same 500 freshly-created codes are resolved twice back-to-back over one reused
HTTP connection: the **cold** pass is a cache miss (Redis miss → Postgres → populate), the
**warm** pass is a cache hit (served from Redis, Postgres untouched).

| metric | cold (miss) | warm (hit) | speedup |
| ------ | ----------- | ---------- | ------- |
| mean   | 176 µs      | 100 µs     | 1.76×   |
| p90    | 489 µs      | 307 µs     | 1.59×   |
| p99    | 653 µs      | 378 µs     | 1.73×   |

Measured on loopback (`docker compose` on one machine), so absolute latencies are
sub-millisecond and the median sits below `curl`'s timer resolution — the meaningful figure
is the consistent ~1.7× reduction at the mean and tail. The bigger production win isn't this
local microsecond delta: a cache **hit never touches Postgres** (verified by resolving a
cached code with Postgres stopped), so the database is shielded from the read-heavy redirect
path and from hot-key stampedes (collapsed via `singleflight`). Cache failures **fail open** —
a Redis outage degrades latency, never correctness. Realistic load numbers, where the DB
carries network latency and contention, come with the k6 suite in Phase 8.
