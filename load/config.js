// Shared config + helpers for the load scripts.
//
// Defaults target the docker-compose stack (nginx on :80, load-balancing the 3 API
// instances) — that's where the Phase 6 Grafana dashboards live, so it's where we
// read the bottleneck. To drive the kind-deployed system through the ingress instead:
//   BASE_URL=http://localhost  HOST_HEADER=shortn.localhost
export const BASE_URL = __ENV.BASE_URL || 'http://localhost';
const HOST_HEADER = __ENV.HOST_HEADER || '';

// The ingress routes by Host header; compose doesn't care. So we only attach a
// Host header when one is set.
export const jsonHeaders = HOST_HEADER
  ? { 'Content-Type': 'application/json', Host: HOST_HEADER }
  : { 'Content-Type': 'application/json' };

// redirects:0 so k6 measures the 302 itself, not the followed hop out to example.com.
export const redirectParams = HOST_HEADER
  ? { redirects: 0, headers: { Host: HOST_HEADER } }
  : { redirects: 0 };

// spread reads across the seeded pool — not one hot code, not all-new
export function randItem(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}
