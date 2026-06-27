# Security Policy

`shortn` is a portfolio/learning project that is nonetheless engineered to be
deployed on the public internet. This document is its **threat model**: what is
defended, what is accepted or deferred risk, and how to report a problem. There
is no SLA; security is handled on a best-effort basis.

## Reporting a vulnerability

Please report security issues **privately** — do not open a public issue.

- Preferred: GitHub's **private vulnerability reporting** (the repo's _Security_
  tab → _Report a vulnerability_).
- Include: what you found, how to reproduce it, and the impact.

You'll get an acknowledgement; fixes are made as time allows and disclosed once a
fix is available.

## The defining architectural fact

**The server never fetches the target (long) URL.** Creating a link stores the
URL; visiting `GET /{code}` returns an HTTP `302` and the **client's browser**
performs the navigation. Nothing server-side ever requests the destination. This
is why classic **SSRF is not currently exploitable** — there is no server-side
request to forge — and it shapes the whole model below.

## Mitigated

| Threat | Mitigation |
| ------ | ---------- |
| **Dangerous-scheme redirect → XSS** (`javascript:`, `data:`, `file:`) | Create accepts only `http`/`https` (scheme allowlist in `internal/shortener`). |
| **Pointing a link at internal/cloud-metadata addresses** | Create rejects IP-literal destinations in private, loopback, link-local (incl. the `169.254.169.254` metadata IP), and unspecified ranges. Hygiene + defense-in-depth, since the server never fetches them. |
| **`Location` / response-header injection (CRLF)** | Go's `net/http` rejects header values containing CR/LF. |
| **Stored/reflected XSS via codes or URLs** | The API responds `application/json` (escaped by `encoding/json`); no user data is rendered as HTML server-side. |
| **CSRF** | The API is stateless with no cookie/session auth → no CSRF surface. |
| **Denial of service / bulk abuse** | Redis-backed distributed token-bucket rate limiter (`429` + `Retry-After`), per-request timeouts, and a circuit breaker (Phase 5). |
| **Secrets in source control** | Secrets are committed only as encrypted **SealedSecrets**; no plaintext credentials in git (Phase 7). |
| **Code enumeration** | Short codes are coordination-free Snowflake IDs obfuscated with `sqids` — non-sequential, not guessable by increment. |

## Accepted or deferred risk (documented on purpose)

- **IP-encoding bypasses** (`http://0177.0.0.1`, `http://2130706433`, some IPv6
  forms) and **DNS rebinding** can defeat *string-level* destination filtering.
  These only matter if the server ever fetches a destination. The correct,
  bulletproof fix is to validate the **actually-connected IP at connect time**
  via a custom dialer `Control` hook — this is **deferred until a server-side
  fetch exists** (e.g. link previews or a Safe Browsing fetch), which is where it
  belongs. The current create-time IP block is intentionally best-effort.
- **Hostnames that resolve to internal IPs** are not blocked (no DNS lookup at
  create time). Same rationale: no server fetch today; the connect-time check is
  the right place when one is added.
- **Open redirect is the product.** A URL shortener redirects anywhere by design.
  Abuse (phishing/malware laundering under a trusted short domain) is throttled by
  rate limiting today; **Google Safe Browsing screening and/or an interstitial
  warning page are planned for the public deployment** (Phase 9).
- **No authentication on link creation.** Acceptable for the current scope and
  throttled by the rate limiter; light auth and/or abuse screening will be added
  before/at a public launch.

## Out of scope

- The local development stack (`docker compose`, the `kind` cluster) is for
  development only and is not hardened for untrusted networks.
- Dependencies are kept current but no formal supply-chain attestation is provided.
