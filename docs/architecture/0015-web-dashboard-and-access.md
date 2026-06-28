# Web dashboard and admin-gated access (0015)

**Status:** Accepted, 2026-06-27

## Context

The system is API-first and anonymous: anyone can create a link, follow a code, or read a
code's click stats. Putting it on the public internet raised two needs. A visitor needs a
human-usable surface to create links and see analytics, not just `curl`. And the management
operations — listing every link and deleting one — must not be open to the world once the API
is publicly reachable.

The system has no user accounts and no session auth, and adding them for a single-operator
demo would be disproportionate.

## Decision

Ship a React single-page application (Vite + TypeScript) built to static files and served by
nginx under `/app`, same-origin with the API. Short links own the root (`/{code}`); the
dashboard lives at `/app`.

Keep the public operations unauthenticated — create, redirect, and per-code stats. The short
code is the capability: knowing it is what grants access to a link's analytics.

Gate the two privileged operations — list all links and delete a link — behind a single
shared admin key sent as the `X-Admin-Key` header. The comparison is constant-time, and the
check fails closed: when no key is configured, the endpoints are locked rather than open.

## Alternatives considered

- **Server-rendered HTML (Go `html/template`)** — rejected. No build step and a simpler
  deploy, but it gives up client-side routing and the interactive analytics charts, and does
  not reuse the existing React strength.
- **A separately hosted frontend (Cloudflare Pages, Vercel) calling the API cross-origin** —
  rejected. It needs CORS, a second deploy target, and exposes the API origin. Serving the SPA
  same-origin under `/app` avoids all three at this scale.
- **Per-user accounts or OAuth for management** — rejected. Real multi-tenant auth, but this
  is a single-operator service. One shared admin key is right-sized; accounts would be
  over-engineering.
- **No frontend (API and `curl` only)** — rejected. Adequate for engineers, but a clickable
  demo is far more legible to a visitor and is the point of a public deployment.

## Consequences

### Good

- The SPA is same-origin, so there is no CORS configuration and no second host to operate.
- Public analytics need no auth: the code-as-key model means a stats request carries its own
  authorization.
- The admin gate is a single environment variable (`ADMIN_KEY`), fails closed, and uses a
  constant-time compare, so it neither leaks timing nor opens by misconfiguration.

### Bad / trade-offs

- A single shared key is coarse: there is no per-user identity or audit, and rotating it means
  a redeploy.
- On the current edge-only TLS, the admin key crosses Cloudflare to the origin in cleartext;
  sensitive management is done over the SSH tunnel until an origin certificate lands (see
  [the deployment topology](../deployment.md)).
- Code-as-key means anyone who knows a code can read its stats. This is acceptable: click
  counts are not sensitive, and codes are non-sequential.

See [the access and analytics model](../explanation/access-and-analytics-model.md), [the
security analysis](../explanation/security-ssrf-and-redirects.md), and
[SECURITY.md](../../SECURITY.md).
