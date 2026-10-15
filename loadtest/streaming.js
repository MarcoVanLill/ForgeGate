import { check } from 'k6';
import { response } from './lib.js';

export const options = { vus: 5, duration: '20s', thresholds: { http_req_failed: ['rate<0.01'], http_req_duration: ['p(95)<2000'] } };
export default function () { const res = response('fake/echo', 'stream under load', true); check(res, { 'SSE completes': (r) => r.status === 200 && r.body.includes('event: response.completed') }); }
