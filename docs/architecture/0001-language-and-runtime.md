# Language and runtime (0001)

**Status:** Accepted, 2026-06-06

## Context

The implementation language is the most load-bearing decision in `shortn`. It constrains container size, the concurrency model, and how well the service fits the cloud-native tooling it runs on (Kubernetes, OpenTelemetry, the Prometheus ecosystem).

The forces in play:

- **Deployment shape.** The system ships tiny, secure container images (see ADR [0003](0003-container-strategy.md)). A language that produces a single, self-contained native binary keeps the final image at ~10–15 MB on a distroless or scratch base, with no interpreter or runtime to drag along.
- **Concurrency.** A URL shortener is I/O-bound and fan-out heavy (DB calls, cache lookups, event publishing). It needs cheap concurrency without a hand-rolled thread pool.
- **Ecosystem fit.** The cloud-native control plane the service depends on (Docker, Kubernetes, Prometheus, etcd, Terraform) is written in this language's ecosystem. Building in that ecosystem means the idioms, client libraries, and documentation all line up.

## Decision

`shortn` is built in **Go**.

Go compiles to a static native binary, ships a goroutine-based concurrency model and a strong standard library (`net/http`, `log/slog`, `context`), and is the lingua franca of cloud-native infrastructure: Docker, Kubernetes, containerd, etcd, Prometheus, Terraform, Helm, and ArgoCD are all written in it, so writing Go integrates with the grain rather than through an adapter layer.

## Alternatives considered

- **Node.js** — rejected. No static binary; the image would carry a Node runtime plus `node_modules` (hundreds of MB, larger attack surface). The single-threaded event loop is fine for I/O but awkward for CPU-bound and fan-out work. Kubernetes client, Prometheus, and Kafka bindings are trailing community implementations of Go APIs.
- **Rust** — rejected. Produces excellent static binaries, but the borrow-checker learning curve is steep and would slow time-to-ship.
- **Java/JVM** — rejected. Heavy runtime and large base images; JVM warmup and memory footprint fight the tiny-container goal. GraalVM native-image narrows the gap but adds significant build complexity.

## Consequences

### Good

- **Tiny, static images.** `CGO_ENABLED=0` yields a fully static binary that runs on `gcr.io/distroless/static` — see ADR [0003](0003-container-strategy.md). Image size is ~10–15 MB versus ~150–200 MB for `node:alpine` or ~100–150 MB for `python:slim`.
- **First-class cloud-native ecosystem.** The k8s client (`k8s.io/client-go`), OpenTelemetry SDK, Prometheus client (`prometheus/client_golang`), and pgx clients are all idiomatic and well-maintained, not bindings over a Go API.
- **Cheap concurrency.** Goroutines map M:N onto OS threads with ~2KB starting stacks, so graceful shutdown and fan-out work are straightforward and run truly in parallel across cores with no GIL.
- **Cross-compilation.** `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build` produces a zero-dependency Linux ARM64 binary from any host OS in one command.
- **Fast feedback loop.** Quick compiles and `go test ./...` keep CI snappy.

### Bad / trade-offs

- **Verbose error handling.** The `if err != nil` pattern is repetitive compared to exceptions. The upside is that error paths are explicit and visible.
- **Less expressive type system** than Rust (no sum types/enums; generics are newer and more limited). Acceptable for this domain.
