# Postman collection

`../postman/shortn.postman_collection.json` covers every API endpoint plus the observability
checks, as an alternative to the `curl` examples in the [API reference](api.md).

## Import

1. Postman → **Import** → drop in `shortn.postman_collection.json`.
2. Start the stack: `make up` (the edge is nginx on `http://localhost`, not the API
   containers directly — those are internal).

## Run order

The collection variable `lastCode` is filled automatically:

1. **API ▸ Create link** — creates a link; its test script saves the new code into `lastCode`.
2. **API ▸ Redirect (302)** and **API ▸ Stats** — reuse `{{lastCode}}`.

Turn off auto-follow redirects before running *Redirect* (Settings → General →
*Automatically follow redirects* = off). Otherwise Postman chases the `302` to the destination
and reports its `200` instead of the redirect.

## Folders

| Folder | What it covers |
| --- | --- |
| **API** | create, redirect, stats, idempotent retry, and the 400/404 error paths |
| **Health & Ops** | `/healthz` (liveness), `/readyz` (readiness), `/metrics` (scrape target) |
| **Observability (Prometheus)** | targets up, the redirect-p99 / cache-hit / consumer-lag queries, and the loaded alert rules |

## Variables

| Variable | Default | Meaning |
| --- | --- | --- |
| `baseUrl` | `http://localhost` | nginx edge — the real entry point |
| `promUrl` | `http://localhost:9090` | Prometheus API/UI |
| `grafanaUrl` | `http://localhost:3000` | Grafana |
| `lastCode` | _(auto)_ | code captured from Create link |
