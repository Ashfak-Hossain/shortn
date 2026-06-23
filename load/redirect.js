import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, jsonHeaders, redirectParams, randItem } from './config.js';

const RATE = Number(__ENV.RATE || 1000); // redirect arrivals/sec 
const DURATION = __ENV.DURATION || '1m';
const POOL = Number(__ENV.POOL || 200); // how many codes to pre-seed and read across

export const options = {
  scenarios: {
    redirects: {
      executor: 'constant-arrival-rate', // OPEN: RATE new reqs/s regardless of latency
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 200, // VUs reserved up front
      maxVUs: 2000, // ceiling if it needs more to sustain RATE
    },
  },
  thresholds: {
    http_req_duration: ['p(99)<50'], // the redirect SLO, as a gate (ms)
    http_req_failed: ['rate<0.01'], // <1% errors
  },
};

// Pre-create the code pool ONCE; k6 shares the return value with every VU.
// Module-level state is per-VU, so a shared pool has to come from setup().
export function setup() {
  const codes = [];
  for (let i = 0; i < POOL; i++) {
    const res = http.post(
      `${BASE_URL}/api/links`,
      JSON.stringify({ url: `https://example.com/seed/${i}` }),
      { headers: jsonHeaders },
    );
    if (res.status === 201) codes.push(res.json('code'));
  }
  if (codes.length === 0) throw new Error(`seed failed — is the stack up at ${BASE_URL}?`);
  console.log(`seeded ${codes.length} codes`);
  return { codes };
}

export default function (data) {
  const code = randItem(data.codes);
  const res = http.get(`${BASE_URL}/${code}`, redirectParams);
  check(res, { 'redirect 302': (r) => r.status === 302 });
}
