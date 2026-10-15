import { check } from 'k6';
import { response } from './lib.js';

export const options = { vus: 1, iterations: 3, thresholds: { http_req_failed: ['rate<0.01'] } };
export default function () { check(response(), { 'status 200': (r) => r.status === 200 }); }
