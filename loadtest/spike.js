import { check } from 'k6';
import { response } from './lib.js';

export const options = {
  stages: [{ duration: '10s', target: 5 }, { duration: '5s', target: 50 }, { duration: '10s', target: 50 }, { duration: '10s', target: 0 }],
  thresholds: { http_req_duration: ['p(99)<2000'], http_req_failed: ['rate<0.10'] },
};
export default function () { const res = response('fake/echo', 'short spike'); check(res, { 'handled or shed': (r) => [200, 429, 503].includes(r.status) }); }
