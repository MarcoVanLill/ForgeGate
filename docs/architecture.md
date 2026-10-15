# Architecture

## Shape and boundaries

ForgeGate is one Go module producing a synchronous `gateway` and asynchronous
`worker`. Packages group operational ownership rather than abstract layers:

- `gateway`: HTTP/SSE translation and stable error contracts;
- `auth`, `ratelimit`: tenant admission;
- `routing`, `providers`: explicit provider selection and failure policy;
- `storage`: PostgreSQL consistency transitions and migrations;
- `jobs`: outbox publication and JetStream consumption;
- `telemetry`: metrics and tracing bootstrap.

The provider interface owns only completion, streaming, health, and normalized
usage/errors. The router owns deadlines, retries, fallback, circuit breaking,
concurrency, and saturation. This makes failure behavior deterministic in tests.

## Synchronous sequence

1. The gateway assigns/preserves a request ID and hashes the bearer key.
2. PostgreSQL resolves the key to an enabled tenant.
3. Redis atomically applies the tenant's fixed one-minute rate window.
4. PostgreSQL atomically reserves one monthly request after checking budgets.
5. The router loads the tenant's ordered provider configuration.
6. It tries bounded safe retries and moves to the next provider on failure.
7. Usage and an input hash are persisted; prompts are never written to logs.

For SSE, fallback can occur only before the first provider chunk. Once bytes from
a provider are exposed, switching providers would corrupt stream semantics.

## Asynchronous invariants

The acceptance transaction inserts exactly one `(tenant_id, idempotency_key)`
job, reserves its budget, and inserts its outbox event. Therefore:

- no accepted job exists without a durable publication obligation;
- a duplicate key returns the original job and does not reserve budget twice;
- a rolled-back budget or outbox insert leaves no visible job.

The publisher claims outbox rows with `SKIP LOCKED`. A publish/commit crash can
duplicate a message. The worker's conditional `queued|retrying -> running`
transition is the idempotency barrier. Terminal jobs acknowledge redelivery
without calling a provider.

## Retry and dead-letter model

Provider retries happen within a bounded routing attempt. A failed job attempt is
then either marked `retrying` and NAKed with delay, or marked `failed`, copied to
`forgegate.jobs.dlq`, and ACKed. Only normalized retry-safe classes qualify.
Invalid/authentication failures do not retry.

## Budgets and cost

Request counters use an atomic monthly PostgreSQL upsert. Reported usage updates
the monthly cost counter. A provider config may include:

```json
{"pricing":{"input_micros_per_million_tokens":500000,"output_micros_per_million_tokens":1500000}}
```

Pricing is intentionally configuration, not hard-coded market data. Unknown
pricing records zero cost. A single in-flight request can take cost slightly past
the threshold because final tokens are unknown before execution; later requests
are rejected. A reservation/refund model is a future tightening.

## Scaling boundaries

Gateway replicas are stateless beyond dependency connections. Workers scale as a
JetStream durable consumer group. PostgreSQL remains the serialization point for
job state and budgets; Redis owns rate-window counters only. A future service
extraction is justified only when a boundary needs independent ownership,
scaling, or failure isolation.

## Cache decision

Response caching is not enabled by default. Prompts may be sensitive and model
outputs may be stochastic or policy-dependent. A future cache requires explicit
tenant/model opt-in, a hash-only key, short TTL, encryption, and separate billing
semantics. Redis is currently used only where shared mutable state is justified.
