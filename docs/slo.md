# Proposed service-level objectives

These are design targets, not performance claims. Adopt them only after a
representative load window establishes a baseline.

| User journey | Good event | Candidate objective |
|---|---|---|
| Synchronous request | Non-5xx terminal response before gateway deadline | 99.9% over 30 days |
| Async acceptance | Durable job/outbox transaction returns 202 or duplicate 200 | 99.9% over 30 days |
| Async completion | Accepted job reaches terminal state inside its class deadline | 99.5% over 30 days |
| Gateway latency | Non-stream response below a measured threshold | Define after baseline |

Exclude invalid credentials/requests, tenant rate/budget rejection, and client
cancellation from availability. Provider failure remains included from the user's
perspective unless the product contract says otherwise.

Use multi-window burn alerts only after traffic volume is sufficient. Queue age
is preferable to raw depth for an async SLI; queue-age instrumentation is a
roadmap item. Paging ownership, maintenance exclusions, and error-budget policy
must be agreed before these objectives become commitments.
