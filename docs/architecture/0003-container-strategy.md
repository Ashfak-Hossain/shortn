# Container strategy (0003)

**Status:** Accepted, 2026-06-06

## Context

`shortn` is delivered as container images: `docker-compose` locally and
Kubernetes in production. Two forces shape how they are built:

- **Image size and security.** Smaller images pull faster, cost less to store, and
  expose less. Every binary, shell, and package in the final image is attack
  surface and a potential CVE to patch. The final image targets under 30 MB.
- **More than one service.** Two binaries, `api` and `analytics`, live side by
  side. Both are Go programs under `cmd/`, and their build recipes must not drift
  apart.

Go makes the size goal achievable because it compiles to a single static binary
(see ADR [0001](0001-language-and-runtime.md)); the runtime is in the binary, so
the final image needs almost nothing else.

## Decision

A multi-stage Docker build whose final stage is `gcr.io/distroless/static:nonroot`,
packaged as one parameterized Dockerfile:

- **Stage 1 (builder):** start from an official `golang` image, download modules,
  and compile a fully static binary with `CGO_ENABLED=0 GOOS=linux`.
- **Stage 2 (final):** `gcr.io/distroless/static:nonroot` copies in only the
  compiled binary and defines the entrypoint. The `:nonroot` tag runs as an
  unprivileged user (UID 65532), so the image is non-root without a separate
  `USER` directive. The bare `gcr.io/distroless/static` tag runs as root.
- A build arg `ARG SERVICE=api` selects which command to compile (`./cmd/${SERVICE}`),
  so the same Dockerfile produces both the `api` and `analytics` images.

> _Illustrative_
>
> ```dockerfile
> ARG SERVICE=api
> FROM golang:1.x AS build
> # ... go mod download; CGO_ENABLED=0 GOOS=linux go build -o /app ./cmd/${SERVICE}
> FROM gcr.io/distroless/static:nonroot
> COPY --from=build /app /app
> ENTRYPOINT ["/app"]
> ```
>
> Build with: `docker build --build-arg SERVICE=api -t shortn-api .`

## Alternatives considered

- **Single-stage `golang` image** — rejected. The build toolchain, source, and Go
  cache ship to production: hundreds of MB and a huge attack surface. Fails the
  < 30 MB target on its own.
- **`scratch` final stage** — rejected. Truly empty, so it has no CA
  certificates (TLS calls to Postgres/Redpanda/HTTPS fail) and no timezone data.
  `distroless/static` is just as minimal but ships CA certs, tzdata, and
  `/etc/passwd`, the few things a static service actually needs.
- **`alpine` final stage** — rejected. Larger than distroless and includes a shell
  and package manager (extra attack surface), plus the historical musl/CGO DNS
  friction. A shell is not needed in production.
- **Per-service Dockerfiles** — rejected. `api` and `analytics` build identically;
  two files would duplicate the recipe and drift. One `ARG SERVICE` Dockerfile is
  the DRY choice and matches the "design once, update later" principle.

## Consequences

### Good

- Tiny images (~10–15 MB), comfortably under the 30 MB target; fast pulls, fast
  pod startup in k8s.
- Smaller attack surface: no shell, no package manager, runs as non-root. Fewer
  CVEs, less to patch.
- One recipe, two services: the `analytics` image is `--build-arg
  SERVICE=analytics` away, with no new Dockerfile.
- CA certs and tzdata present, so outbound TLS and timestamps work, unlike
  `scratch`.

### Bad / trade-offs

- No shell in the container, so `docker exec ... sh` cannot poke around; debugging
  relies on logs, metrics, and (in k8s) ephemeral debug containers. This is a
  security feature, but it changes debugging habits.
- Static-only constraint: `CGO_ENABLED=0` must hold. Any future dependency needing
  cgo would force a different base image and likely a new ADR.
- Multi-stage builds are less obvious than a single `FROM`, and layer caching
  across stages needs care: copy `go.mod`/`go.sum` and run `go mod download` before
  the source to cache module downloads.
