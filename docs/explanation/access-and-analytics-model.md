# Access and analytics model

This is the conceptual companion to [ARCHITECTURE.md](../../ARCHITECTURE.md). That
document maps the system; this one explains who is allowed to do what, and why the
access model is as small as it is. `shortn` has no user accounts and no login. Most of
its surface is open to anyone; exactly two operations are privileged, gated by a single
shared operator key. The reasoning matters as much as the rules, because the temptation
when extending this service is to reach for per-user auth it does not need.

## Anonymous by design

`shortn` is a single-tenant demo service. There is one operator (the person running it)
and an unbounded set of anonymous visitors. There is no concept of a logged-in user, no
session, no token tied to an identity, and nothing in the data model records who created
a link. A link is a `(code, long_url, created_at)` row; click analytics are
`(code, hour_bucket, count)` aggregates. Neither carries an owner.

This shapes every endpoint. Creating a link, following a code, and reading a code's
stats are all unauthenticated. Anyone who can reach the service can do them. The handlers
in `internal/http/links.go` take no credential and check none: `createLink` validates the
URL and persists it, `redirect` resolves the code and 302s, `stats` reads the aggregate.

Per-user accounts would be over-engineering here. They would add a registration flow, a
session store, password handling, and an ownership column on every row — a large surface
to secure and operate — to serve a service that has exactly one privileged actor. The
honest model for a single-tenant demo is to make the public operations public and put one
lock on the operator operations.

## The code is the key

Analytics are public, scoped per code. `GET /api/links/{code}/stats` returns the click
total and a per-hour series for one short code, and the short code itself is the only
thing protecting it. Knowing the code is the credential. There is no other secret, and no
way to enumerate stats across links from this endpoint — you ask about one code at a time,
and you must already hold that code.

The shape of the response (`internal/shortener/shortener.go`) is deliberately narrow:

- `Total` — clicks summed from `click_events`, not a denormalized counter on the link row.
- `Series` — a list of `ClickBucket{Bucket, Count}`, one per hour, oldest first
  (`date_trunc('hour', ts)` in Postgres).

That is the entire analytics surface. There are no per-visitor records, no referrer logs,
no IP history, no geo breakdown. The system aggregates clicks into hourly buckets at
write time and never retains the individual click beyond what the exactly-once consumer
needs to count it. So "public per code" leaks click volume over time for a code someone
already knows — and nothing about who clicked, because that data does not exist.

This is the same property the codes themselves rely on. Short codes are Snowflake-derived
and sqids-encoded (see [distributed IDs](distributed-ids.md)); they are not sequential, so
a code is not trivially guessable from a neighboring one. The code is unguessable enough
to act as a capability and cheap enough to share in a URL. That is the whole access
control for read paths.

## The two privileged operations

Two endpoints are not public:

- `GET /api/links` — list every link, newest first, paged with `?limit` and `?offset`
  (`defaultListLimit` 50, `maxListLimit` 100). This is the operator's view of the whole
  table; on the live deployment it backs the dashboard's management screen.
- `DELETE /api/links/{code}` — remove a link. Returns `204` on success, `404` when the
  code is unknown.

These are privileged for the obvious reason: listing every link defeats the
code-is-the-key model for reads (it hands an attacker the full set of codes), and delete
is destructive. Everything else stays open; only the operations that would expose the
whole table or mutate it are locked.

## How the lock works

Both privileged routes sit in a sub-group in `NewRouter`
(`internal/http/router.go`) wrapped by `AdminAuthMiddleware`
(`internal/http/middleware.go`). The middleware reads the `X-Admin-Key` request header
and compares it to the server's configured `ADMIN_KEY`. Three details define its behavior:

- **Fail-closed.** When no key is configured on the server (`adminKey == ""`), every admin
  request is rejected with `401`. An unset key locks the endpoints rather than opening
  them. Forgetting to set the key locks the door; it does not remove it. This is the
  single most important property of the check — the safe default is "denied."
- **Constant-time comparison.** The match uses `subtle.ConstantTimeCompare`, not `==`, so
  the comparison time does not depend on how many leading bytes matched. A `==` check
  returns faster on an early mismatch, and an attacker can measure that to recover the key
  byte by byte. The constant-time compare removes that timing channel.
- **One shared secret, no accounts.** There is exactly one operator key, not a per-user
  credential. With a single privileged actor, one shared secret is the right-sized control:
  enough to keep the public internet out of admin operations without standing up real
  authentication.

A rejected request gets `401` with `admin authentication required`. The admin routes are
still inside the rate-limited group, so the gate sits behind the limiter and a brute-force
attempt against the key is throttled like any other client.

## Where this sits in the deployment

On the live deployment ([shortn.ashfak.dev](https://shortn.ashfak.dev)) this is exactly
how the React dashboard's management view is gated. The dashboard holds the admin key and
sends it as `X-Admin-Key` on list and delete calls; the public create/redirect/stats paths
need no key. The same `ADMIN_KEY` env var feeds the API pods.

The admin gate is one layer of the public-internet defense, not all of it. The other
layers are documented where they live:

- The rate limiter keys on the originating client IP, read from the right-most
  `X-Forwarded-For` entry that the single trusted proxy appends — spoof-resistant because
  a forged header only prepends entries. On Azure the public profile tightens this to 20
  rps sustained, 40 burst per client (`values-azure.yaml`). See `internal/http/ratelimit.go`.
- Liveness, readiness, and the Prometheus scrape live on a separate ops listener
  (`NewOpsRouter`) that the ingress never routes, keeping `/metrics` and `/readyz` off the
  public surface entirely.
- Create-time URL validation blocks SSRF/internal-address targets in `normalizeURL`. The
  threat model — which attacks are live for this architecture and which are accepted or
  deferred — is in [SSRF and open redirects](security-ssrf-and-redirects.md).

## The honest limitations

- **A leaked code leaks its stats forever.** There is no rotation, no expiry, no
  revocation of read access short of deleting the link. The code is a bearer capability;
  anyone it reaches can read that link's click volume.
- **One key, no per-operator audit.** A single shared admin key means there is no way to
  tell which operator listed or deleted, and rotating it means updating every holder at
  once. Acceptable for one operator; it would not be for a team.
- **No abuse attribution on create.** Because create is anonymous, abuse is bounded only by
  the rate limiter and the SSRF validation, not traced to an account. That is the
  deliberate cost of having no accounts.

These are consequences of the single-tenant model, not gaps in it. The model is sized for
exactly one operator and an anonymous public. Adding accounts later means adding an owner
column, a real auth system, and per-owner scoping on every read path — a different service,
deliberately not built here.

## See also

- [ARCHITECTURE.md](../../ARCHITECTURE.md) — the system map this doc explains.
- [ADR 0015 — Web dashboard and admin-gated access](../architecture/0015-web-dashboard-and-access.md) — the decision this model records.
- [SSRF and open redirects](security-ssrf-and-redirects.md) — the create-time threat model
  and the rest of the public-internet defenses.
- [Distributed IDs](distributed-ids.md) — why short codes are unguessable enough to act as
  read capabilities.
