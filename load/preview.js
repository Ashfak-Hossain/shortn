// Preview load for the Phase 6 dashboards — NOT a real load test (that's Phase 8).
// It drives a steady mix of redirects (the hot path) and a trickle of creates so
// the golden-signal panels move and the cache-hit ratio climbs as links warm.
//
// Run with `make load` (k6 via Docker — no local install needed). The stack must
// be up first (`make up`).
import http from 'k6/http';
import { check, sleep } from 'k6';

// BASE_URL is injected by `make load`. Default works if you run k6 locally.
const BASE = __ENV.BASE_URL || 'http://localhost';

export const options = {
  vus: Number(__ENV.VUS || 10),
  duration: __ENV.DURATION || '2m',
};

// setup() runs ONCE before the VUs and its return value is shared with every VU
// (module-level state in k6 is per-VU, so a shared pool has to come from here).
// Seeding a small set of codes makes the redirect stream below mostly cache HITS.
export function setup() {
  const codes = [];
  for (let i = 0; i < 15; i++) {
    const res = http.post(
      `${BASE}/api/links`,
      JSON.stringify({ url: `https://example.com/seed/${i}` }),
      { headers: { 'Content-Type': 'application/json' } },
    );
    if (res.status === 201) codes.push(res.json('code'));
  }
  return { codes };
}

export default function (data) {
  if (Math.random() < 0.1 || data.codes.length === 0) {
    // ~10%: create a fresh link and read it once — a guaranteed cache MISS + DB load.
    const res = http.post(
      `${BASE}/api/links`,
      JSON.stringify({ url: `https://example.com/${Date.now()}-${__VU}` }),
      { headers: { 'Content-Type': 'application/json' } },
    );
    check(res, { 'create 201': (r) => r.status === 201 });
    if (res.status === 201) {
      // redirects:0 so k6 does NOT follow the 302 out to example.com.
      http.get(`${BASE}/${res.json('code')}`, { redirects: 0 });
    }
  } else {
    // ~90%: redirect on a warm seeded code — a cache HIT.
    const code = data.codes[Math.floor(Math.random() * data.codes.length)];
    const res = http.get(`${BASE}/${code}`, { redirects: 0 });
    check(res, { 'redirect 302': (r) => r.status === 302 });
  }
  sleep(0.3);
}
