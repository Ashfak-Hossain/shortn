# shortn — dashboard (`web/`)

The React frontend for **shortn**, a URL shortener. It talks to the Go API to create short
links, manage them (list + delete), and view per-link click analytics.

## Stack

- **Vite** — dev server + build
- **React 19 + TypeScript**
- **Tailwind CSS v4** — styling (via the Vite plugin)
- **React Router v7** — client-side routing
- **TanStack Query v5** — server state (fetching, caching, refetch-after-mutation)
- **Recharts** — the clicks-over-time chart

## Prerequisites

- Node 20+ and npm
- The **backend running**, so the dashboard has an API to call. From the repo root:
  `make up-build` — the compose stack serves the API behind nginx on `http://localhost:80`.

## Develop

```sh
npm install
npm run dev      # http://localhost:5173
```

The dev server **proxies `/api` → `http://localhost:80`** (see `vite.config.ts`), so the
browser sees a single origin and there is **no CORS** — the same shape as production behind the
ingress. Keep the compose stack up while developing.

## Configuration

The API base URL is baked in **at build time** (a static site has no server to read env at
runtime):

- `VITE_API_BASE_URL` — leave **empty** to call the API on the **same origin** (the dev proxy,
  or prod behind the ingress). Set it only if the frontend is ever served from a different
  origin than the API. See `.env.example`.

## Structure

```text
src/
  api/           # the ONLY place that knows API URLs + methods
    client.ts    # typed fetch wrapper (base URL, error handling, 204)
    links.ts     # createLink / listLinks / deleteLink / getStats
  hooks/
    useLinks.ts  # TanStack Query hooks (query + mutations + cache invalidation)
  components/     # CreateForm, LinksTable, ClicksChart
  pages/          # CreatePage, LinksPage, AnalyticsPage (one per route)
  types.ts        # TS mirror of the API's JSON contract
  App.tsx         # layout + routes
  main.tsx        # providers: BrowserRouter + QueryClientProvider
```

## Scripts

- `npm run dev` — dev server with hot reload (HMR)
- `npm run build` — type-check + production build to `dist/`
- `npm run preview` — serve the built `dist/` locally
- `npm run lint` — ESLint

## Production

`npm run build` emits static files to `dist/`. In this project they are served behind the same
ingress as the API (same origin), packaged into the Helm chart — see Phase 9, Submodule 9.5.
