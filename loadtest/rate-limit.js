import { check } from 'k6';
import { Rate } from 'k6/metrics';
import { response } from './lib.js';

const rejected = new Rate('rate_limit_rejected');
export const options = { vus: 20, iterations: 40, thresholds: { rate_limit_rejected: ['rate>0.50'] } };
export default function () { const res = response('fake/echo', 'rate limit'); rejected.add(res.status === 429); check(res, { 'bounded response': (r) => r.status === 200 || r.status === 429 }); }
