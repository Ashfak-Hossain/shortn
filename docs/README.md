# Documentation

Reference and design documentation for `shortn`, organized by what you need.

## Start here

- [Architecture](../ARCHITECTURE.md) — the system overview: diagrams, layered design, the two
  request paths.
- [Request lifecycle](request-lifecycle.md) — a create and a redirect traced end to end.

## Decisions

- [Architecture Decision Records](architecture/README.md) — every significant choice as
  Context, Decision, Alternatives, and Consequences.

## Explanation

How and why each subsystem works.

- [Clean architecture and dependency injection](explanation/clean-architecture-and-di.md)
- [Distributed IDs](explanation/distributed-ids.md)
- [Messaging and exactly-once](explanation/messaging-and-exactly-once.md)
- [Observability](explanation/observability.md)
- [Resilience](../ARCHITECTURE.md#cross-cutting-properties) (decision in [ADR 0009](architecture/0009-resilience.md))
- [Security: SSRF and redirects](explanation/security-ssrf-and-redirects.md)
- [Orchestration and delivery](explanation/orchestration-and-delivery.md)
- [Data durability](explanation/data-durability.md)
- [Access and analytics model](explanation/access-and-analytics-model.md)

## Reference

- [API](reference/api.md) — endpoints, request/response shapes, status codes.
- [Services and ports](reference/services-and-ports.md) — the processes and the ports they use.
- [Postman collection](reference/api-postman.md) — importing and running the API requests.

## Operations

- [Deployment](deployment.md) — the live production topology and how it is run.
- [Runbook](runbook.md) — failure modes, health checks, backups, and SLOs.
- [Performance](performance.md) — load-test method and results.

## Project

- [Security policy](../SECURITY.md) — threat model and responsible disclosure.
- [Contributing](../CONTRIBUTING.md) — build, test, and the conventions to follow.
- [Changelog](../CHANGELOG.md) — the capabilities the system gained over time.

## Standards

- [Go doc comments](standards/go-doc-comments.md) — the doc-comment style the code follows.
