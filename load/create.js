import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, jsonHeaders } from './config.js';

const RATE = Number(__ENV.RATE || 200); // create arrivals/sec (open model)
const DURATION = __ENV.DURATION || '1m';

export const options = {
  scenarios: {
    creates: {
      executor: 'constant-arrival-rate', // OPEN model, same as redirect.js
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 200,
      maxVUs: 2000,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'], // <1% errors (no latency SLO — writes are the slow path)
  },
};

// No setup()/pool — each iteration is a fresh create. The URL is made unique with
// __VU + __ITER (+ time) so every POST is a real insert, never an idempotent no-op.
export default function () {
  const url = `https://example.com/${__VU}-${__ITER}-${Date.now()}`;
  const res = http.post(`${BASE_URL}/api/links`, JSON.stringify({ url }), { headers: jsonHeaders });
  check(res, { 'create 201': (r) => r.status === 201 });
}
