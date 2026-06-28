# SSRF and redirects

A URL shortener is the textbook example of an SSRF and open-redirect target, so it
is worth being precise about which of those threats is actually live for `shortn`
and which are accepted or deferred risk. This note explains the reasoning. The
threat-model table (what is mitigated, accepted, or out of scope) and the
responsible-disclosure policy live in [SECURITY.md](../../SECURITY.md); the
create-time validation it describes is in
[`internal/shortener/shortener.go`](../../internal/shortener/shortener.go).

## The one fact that shapes the model

`shortn` never fetches the target URL on the server. Creating a link stores the
long URL as a string; `GET /{code}` returns a `302` with a `Location` header, and
the client's browser performs the navigation. The analytics consumer reads click
events, never the destination. Nothing server-side ever issues a request to the
URL a user shortened.

That single fact decides most of what follows, because classic SSRF requires the
server to make the request.

```text
classic SSRF (not shortn):                  shortn:
attacker → server → http://169.254.169.254  attacker → server (stores the string)
          (server fetches the internal IP)   victim   → server → 302 → victim's browser fetches
```

## Why SSRF is not currently exploitable

Server-Side Request Forgery tricks a server into making an HTTP request to
somewhere it shouldn't reach: the cloud metadata endpoint
(`http://169.254.169.254/latest/meta-data/`, which returns IAM credentials on
AWS), an internal admin panel (`http://10.0.0.5:8080`), a database on
`localhost`. The server has network access the attacker does not, so the server's
request is the exploit.

For `shortn` there is no such request. An attacker who shortens
`http://169.254.169.254/latest/meta-data/` gains nothing: the link sits in
Postgres until someone clicks it, and then the visitor's browser, which cannot
reach the cloud-metadata IP from the open internet, tries to load it. There is no
server-side request to forge.

This stops being true the instant the server fetches a destination itself. Link
previews and unfurling (fetch the page for its `<title>` or OpenGraph image), a
Safe Browsing check implemented as a fetch, favicon grabbing, screenshot
generation, and destination health-checking all introduce a server-side request.
Any of those would make every SSRF vector real, which is why the connect-time
defense is deferred rather than absent (see the limits below).

## Open redirect is the product

The usual open-redirect bug is a `?next=` parameter that blindly redirects to an
attacker-supplied URL, letting a phisher send a link on a trusted domain that
bounces to a fake login page. A shortener redirects anywhere by design; that is
the entire job. So "it's an open redirect" is true and beside the point, because
refusing to redirect would mean refusing to be a shortener.

Three concrete threats hide under that label, and they get different treatment:

- **Dangerous-scheme redirects.** A destination like `javascript:alert(1)` or
  `data:text/html,...` becomes script execution on the origin if a browser ever
  puts it in a `Location`, or a future frontend renders it as an `href`. This is
  mitigated at create time: `normalizeURL` accepts only `http` and `https`
  schemes (lower-cased first, so `HTTP://` is normalized rather than rejected).
- **Phishing and malware laundering.** A trusted short domain wrapping a malicious
  destination is an abuse and reputation problem, not a memory-safety bug. The
  current defense is the rate limiter, which throttles bulk creation.
  Google Safe Browsing screening and an interstitial warning page are planned for
  the public deployment, not yet built.
- **Pointing a victim's browser at internal addresses.** A destination like
  `http://192.168.1.1/admin/reboot` shortened and sent to someone inside a
  corporate network has the victim's browser do the work, so it is not strictly a
  vulnerability in `shortn`. Refusing obviously internal destinations is cheap
  hygiene, so the create-time IP block does it anyway.

## The create-time IP block

`normalizeURL` parses the host with `u.Hostname()` (which strips port and IPv6
brackets) and runs `net.ParseIP` on it. `ParseIP` returns `nil` for a real
hostname, so only IP-literal destinations reach the range check; hostnames pass
through unexamined (DNS is not resolved at create time, by design). When the host
is an IP literal, `isBlockedIP` rejects it:

```go
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}
```

Each class is checked explicitly because `net.IP.IsPrivate()` alone is not
enough. `IsPrivate()` covers RFC 1918 (`10/8`, `172.16/12`, `192.168/16`) and
`fc00::/7`, but misses loopback (`127/8`, `::1`), link-local
(`169.254/16` — which includes the `169.254.169.254` metadata IP — and `fe80::/10`),
and the unspecified address (`0.0.0.0`, `::`). `IsLinkLocalUnicast` is what
catches the metadata IP; `IsMulticast` covers link-local multicast. Relying on
`IsGlobalUnicast()` instead would not help, since it returns true for `10.x` as
well.

The block does no I/O and resolves no names, so it is cheap and cannot itself be
an SSRF vector. Its purpose is hygiene and future-proofing, not a complete SSRF
defense.

## The honest limits

The create-time check is best-effort by construction, and two gaps are worth
naming rather than hiding:

- **IP-encoding bypasses.** Forms like `http://0177.0.0.1` (octal),
  `http://2130706433` (dword), and `http://[::ffff:127.0.0.1]` can resolve to
  loopback in a browser, but `net.ParseIP` rejects the unusual encodings, so they
  slip past the IP-literal check. Chasing every octal, dword, and IPv6 encoding at
  the string layer is a losing game.
- **DNS rebinding.** A hostname that resolves to a public IP at create time can be
  re-pointed at `127.0.0.1` afterward. Validating DNS at create time cannot close
  this time-of-check / time-of-use gap.

Both are solved properly by checking the IP that is actually connected to, at
connect time, through a custom dialer `Control` hook on the HTTP transport. That
check only exists where there is a connection to make, so it belongs at a
server-side fetch. Because `shortn` has no such fetch today, that defense is
deferred to the day fetching is added, and the create-time block stands in for it
as the cheap, honest interim. SECURITY.md records this as accepted risk and names
the trigger to upgrade.

## Adjacent vulnerabilities, briefly

Several common web vulnerabilities are handled by the architecture rather than by
dedicated code:

- **Reflected or stored XSS.** The API responds `application/json`, which Go's
  `encoding/json` escapes; no user data is rendered as HTML server-side. This is
  worth revisiting where the React dashboard renders user-supplied URLs.
- **`Location` / response-header CRLF injection.** Go's `net/http` rejects header
  values containing CR or LF, so a destination cannot inject extra headers.
- **CSRF.** The API is stateless with no cookie or session auth, so there is no
  CSRF surface.
- **Code enumeration.** Short codes are Snowflake IDs obfuscated with `sqids`, so
  they are non-sequential and not guessable by increment.
- **DoS and bulk abuse.** The Redis-backed token-bucket rate limiter (`429` +
  `Retry-After`), per-request timeouts, and the circuit breaker
  throttle abuse.

## See also

- [SECURITY.md](../../SECURITY.md) — the threat-model table and disclosure policy.
- [`internal/shortener/shortener.go`](../../internal/shortener/shortener.go) —
  `normalizeURL` and `isBlockedIP`.
- [docs/runbook.md](../runbook.md) — rate limiting and abuse responses.
