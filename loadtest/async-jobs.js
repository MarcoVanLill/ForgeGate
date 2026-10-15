import http from 'k6/http';
import { check } from 'k6';
import { baseURL, headers } from './lib.js';

export const options = { vus: 3, iterations: 15, thresholds: { http_req_failed: ['rate<0.01'], http_req_duration: ['p(95)<750'] } };
export default function () {
  const jobHeaders = Object.assign({}, headers, { 'Idempotency-Key': `k6-${__VU}-${__ITER}` });
  const created = http.post(`${baseURL}/v1/jobs`, JSON.stringify({ model: 'fake/echo', input: 'async load' }), { headers: jobHeaders });
  check(created, { 'job accepted': (r) => r.status === 202 });
  if (created.status === 202) { const fetched = http.get(`${baseURL}/v1/jobs/${created.json('id')}`, { headers }); check(fetched, { 'job visible': (r) => r.status === 200 }); }
}
