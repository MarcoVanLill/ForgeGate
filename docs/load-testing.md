# Load and reliability testing

| Script | Purpose |
|---|---|
| `steady.js` | Constant synchronous arrival rate |
| `streaming.js` | Concurrent SSE connections |
| `rate-limit.js` | Distributed tenant rejection behavior |
| `async-jobs.js` | Durable submission and visibility |
| `spike.js` | Short ramp/spike and controlled shedding |
| `provider-failure.js` | Injected primary failure and fallback |
| `smoke.js` | Three-request CI sanity check |

Run in isolation. Use separate tenants for throughput and rate-limit scenarios.
Record the exact commit, host/cloud shape, dependency versions, provider mode,
reset procedure, date, and command.

```bash
k6 run -e FORGEGATE_URL=http://localhost:8080 loadtest/steady.js
```

No benchmark has been recorded yet.

| Field | Value |
|---|---|
| Commit | Not measured |
| Environment | Not measured |
| Scenario | Not measured |
| Request rate / VUs | Not measured |
| p50 / p95 / p99 | Not measured |
| Error/rejection rate | Not measured |
| Queue depth/age | Not measured |
| CPU/memory/DB connections | Not measured |

Separate intentional 429/503 shedding from unexpected failures. Never present
fake-provider latency as evidence of external model latency.
