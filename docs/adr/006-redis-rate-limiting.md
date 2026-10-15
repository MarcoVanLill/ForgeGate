# ADR-006: Use Redis for distributed tenant rate limits

Status: accepted.

Gateway replicas need one shared rate decision. A Redis Lua operation atomically
increments a tenant/minute counter and sets its TTL. PostgreSQL owns durable
monthly budgets. The limiter fails closed because silently bypassing it can
create unbounded abuse and cost.
