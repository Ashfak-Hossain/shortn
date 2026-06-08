# shortn

A distributed URL shortener built as a practice project of distributed systems and DevOps concepts.

**Stack:** Go · PostgreSQL · Redis · Docker · GitHub Actions · React

## Status

Phase 0 complete — a containerized Go service with `/healthz` + `/readyz`, JSON logging, env-based config, graceful shutdown, and green CI (lint/test/build).
Next: **Phase 1 — core URL shortener** (Postgres-backed create + redirect).

## How to run

Requires Go 1.26+, Docker, and `make`.

```sh
make run        # start the API on :8080 (PORT, LOG_LEVEL, ENV read from env)
make test       # go test ./...
make lint       # go vet + golangci-lint
make docker     # build the container image (< 30MB)
```

Confirm it's up:

```sh
curl -i localhost:8080/healthz   # 200 — liveness
curl -i localhost:8080/readyz    # 200 — readiness
```

## Architecture

_Coming soon — C4 diagrams will be added in upcoming Phase._

## Performance

_Coming soon — benchmarks will be recorded in upcoming Phase._
