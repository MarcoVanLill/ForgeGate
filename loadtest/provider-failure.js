import { check } from 'k6';
import { Rate } from 'k6/metrics';
import { response } from './lib.js';

const fallbackSucceeded = new Rate('fallback_succeeded');
export const options = { vus: 2, iterations: 10, thresholds: { fallback_succeeded: ['rate>0.99'] } };
export default function () { const res = response('fake/fallback', 'failure injection'); const ok = res.status === 200 && res.json('provider') === 'fake-secondary'; fallbackSucceeded.add(ok); check(res, { 'fell back': () => ok }); }
