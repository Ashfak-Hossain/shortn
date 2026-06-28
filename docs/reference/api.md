# API reference

The HTTP contract for the `shortn` API. All responses are `application/json` except the
redirect. Errors share one shape:

```json
{ "error": "human-readable message" }
```

## Conventions

- **Base URL.** Local: `http://localhost` (behind nginx). Live: `https://shortn.ashfak.dev`.
- **Rate limiting.** Client endpoints are behind a per-client token-bucket limiter. Over the
  limit returns `429 Too Many Requests` with a `Retry-After` header.
- **Admin auth.** Listing and deleting links require an `X-Admin-Key` header. A missing or
  wrong key returns `401`, and the endpoints are locked when no key is configured (fail-closed).
- **Service degradation.** A blown request deadline or an open circuit breaker returns `503`.
- **Access model.** Creating, following, and reading a code's stats are public; the code is the
  key to its analytics. Only list and delete are privileged. See
  [the access model](../explanation/access-and-analytics-model.md).

## Create a link

```
POST /api/links
```

| | |
| --- | --- |
| Auth | none |
| Body | `{"url": "https://example.com"}` |
| Header (optional) | `Idempotency-Key: <token>` — a repeat with the same key returns the original code instead of minting a new one |

**`201 Created`**

```json
{ "code": "Ab3xK9p", "short_url": "http://localhost/Ab3xK9p", "long_url": "https://example.com" }
```

| Status | When |
| --- | --- |
| `400` | Body is not valid JSON, `url` is missing, or the URL is rejected (non-`http(s)`, or it resolves to a private/loopback/link-local/metadata address) |
| `429` | Rate limited (`Retry-After` set) |
| `503` | Postgres unavailable (breaker open or deadline exceeded) |

## Follow a link

```
GET /{code}
```

**`302 Found`** with the original URL in `Location`. A `302` (not `301`) is used so browsers do
not cache the redirect and bypass click tracking. The click is recorded asynchronously after
the response is sent.

| Status | When |
| --- | --- |
| `404` | No link for that code |
| `503` | Postgres unavailable on a cache miss |

## Read a code's stats

```
GET /api/links/{code}/stats
```

| | |
| --- | --- |
| Auth | none (the code is the key) |

**`200 OK`**

```json
{
  "code": "Ab3xK9p",
  "total": 1280,
  "series": [{ "bucket": "2026-06-28T00:00:00Z", "count": 42 }]
}
```

`total` is the all-time click count; `series` is a time-bucketed history.

## List links (admin)

```
GET /api/links?limit=50&offset=0
```

| | |
| --- | --- |
| Auth | `X-Admin-Key` required |
| Query | `limit` (default 50, max 100), `offset` (default 0) |

**`200 OK`** — newest first:

```json
{
  "links": [
    {
      "code": "Ab3xK9p",
      "short_url": "http://localhost/Ab3xK9p",
      "long_url": "https://example.com",
      "created_at": "2026-06-28T12:00:00Z"
    }
  ]
}
```

| Status | When |
| --- | --- |
| `401` | Missing or wrong `X-Admin-Key` |

## Delete a link (admin)

```
DELETE /api/links/{code}
```

| | |
| --- | --- |
| Auth | `X-Admin-Key` required |

**`204 No Content`** on success.

| Status | When |
| --- | --- |
| `401` | Missing or wrong `X-Admin-Key` |
| `404` | No link for that code |

## See also

- [Architecture](../../ARCHITECTURE.md) and the [request lifecycle](../request-lifecycle.md).
- The endpoints are also exercised by a [Postman collection](api-postman.md).
