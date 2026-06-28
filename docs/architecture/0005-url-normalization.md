# URL normalization (0005)

**Status:** Accepted, 2026-06-11

## Context

`POST /api/links` accepts an arbitrary user-supplied URL and stores it as the redirect target. Two questions decide the policy: what input is rejected (validation), and what input is rewritten to a canonical form before storing (normalization). Both affect correctness and security:

- Reject too little and we store junk — or dangerous targets such as `javascript:` URLs.
- Normalize too aggressively and we redirect users somewhere they didn't intend.

A URL has case-insensitive parts (scheme, host) and case-/order-sensitive parts (path, query, fragment); a correct policy treats them differently.

## Decision

The domain (`internal/shortener`) owns URL rules — not the HTTP handler — because they are business rules that any caller (a future CLI, the analytics service) must get identically.

**Reject** (→ `ErrInvalidURL`, surfaced as HTTP `400`):

- empty / whitespace-only input,
- anything that doesn't parse,
- any scheme other than `http`/`https` (so `javascript:`, `mailto:`, `ftp:`, `data:` are all refused),
- a URL with no host.

**Normalize** (rewrite, then store):

- lowercase the scheme,
- lowercase the host,
- preserve path, query, and fragment exactly.

Schemeless input (`example.com`) is rejected, not auto-prefixed with `http://`.

The HTTP layer does only transport-level checks (is the body valid JSON, is the `url` field present); everything semantic happens in the domain.

## Alternatives considered

- **Validate/normalize in the HTTP handler** — rejected. URL rules are domain logic; putting them in the transport layer would duplicate them for any non-HTTP caller and blur the layer boundary.
- **Normalize aggressively** (strip the fragment, drop trailing slashes, sort query params, lowercase the whole URL) — rejected. Paths are case-sensitive on many servers, query order can be significant, and the fragment can drive client-side routing (hash-routed SPAs). Rewriting any of these can send a user to a different resource than they pasted. Normalization touches only the parts that are definitionally case-insensitive.
- **Auto-prefix schemeless input** with `http://` — rejected. It guesses user intent and silently downgrades to insecure `http`. Rejecting and asking for an explicit scheme is safer and clearer. (A future UX nicety could prompt the user instead.)

## Consequences

**Good**

- **Security.** Refusing non-`http(s)` schemes blocks `javascript:`/`data:` redirect targets — a real XSS/abuse vector for a redirector.
- **Predictable, minimal canonicalization.** Lowercasing only scheme+host means a stored URL always redirects exactly where the user meant.
- **One home for the rules.** Any caller of the domain gets identical validation, and the rules are unit-tested without HTTP.

**Bad / trade-offs**

- **No same-URL dedup from normalization.** Because we don't aggressively canonicalize, `https://example.com` and `https://example.com/` are treated as distinct. Acceptable — over-normalizing is riskier than storing near-duplicates.
- **No length cap.** `long_url` is unbounded `TEXT`, so a very large URL is accepted. Known limitation: hardening it would take a `MaxBytesReader` on the handler plus a domain length check.
- **`url.Parse` is lenient.** It accepts much that isn't a "real" URL, so the explicit scheme/host checks, not the parse error, do the actual rejecting. The system relies on those checks being correct, which the unit tests cover.
