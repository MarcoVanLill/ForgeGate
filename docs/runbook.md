# Operations runbook

## First checks

1. Check `/health/live`; failure means the process is not serving.
2. Check `/health/ready`; identify PostgreSQL, Redis, or NATS failure.
3. Inspect HTTP rates/latency, provider outcomes, circuits, queue depth, and jobs.
4. Correlate structured logs and traces by `request_id` or `job.id`.
5. Preserve evidence before restarting or altering queue state.

## PostgreSQL unavailable

Readiness fails, authentication/job APIs return 5xx, and workers NAK. Verify
DNS/TLS/credential expiry and connection saturation. Restore PostgreSQL before
Redis/NATS because it is the consistency authority. After recovery, confirm
outbox rows drain and jobs do not remain indefinitely `running`. Never mark
outbox rows published without evidence.

## Redis unavailable

Rate limiting fails closed with `rate_limiter_unavailable`; already accepted jobs
continue. Restore Redis and confirm metrics normalize. Do not bypass limiting
unless an incident commander accepts tenant-abuse risk.

## NATS unavailable

Synchronous inference continues. Async jobs remain durable in PostgreSQL with
unpublished outbox rows. Restore JetStream and observe the outbox/backlog drain.
Do not bulk republish from `jobs` while the outbox publisher is active.

## Provider failure

Confirm failure/fallback counts and circuit state. Check tenant routing and secret
references without printing secrets. Disable a provider only through an audited
operation. A circuit closes after successful half-open traffic.

## Queue backlog

Compare pending count, processing duration, provider latency, and DB pool
pressure. Scale workers only within provider/DB concurrency budgets. Treat DLQ
payloads as sensitive. Redrive only after fixing the cause and preserving the
original job/audit trail.

## Safe shutdown and rollback

Send SIGTERM and allow the configured timeout. Gateway drains HTTP; the worker
stops fetching and unacknowledged work redelivers. Roll back application images
before destructive schema changes; current migrations are additive/forward-only.

```bash
docker compose ps
docker compose logs --since=10m gateway worker
curl -fsS localhost:8080/health/ready
curl -fsS localhost:8080/metrics | grep '^forgegate_'
curl -fsS localhost:8222/jsz
```
