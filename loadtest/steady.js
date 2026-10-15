import { check, sleep } from 'k6';
import { response } from './lib.js';

export const options = {
  scenarios: { steady: { executor: 'constant-arrival-rate', rate: 5, timeUnit: '1s', duration: '30s', preAllocatedVUs: 5, maxVUs: 20 } },
  thresholds: { http_req_failed: ['rate<0.01'], http_req_duration: ['p(95)<500'] },
};
export default function () { const res = response(); check(res, { 'completed': (r) => r.status === 200 && r.json('status') === 'completed' }); sleep(0.1); }
