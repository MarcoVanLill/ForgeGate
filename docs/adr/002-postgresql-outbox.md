# ADR-002: Use a PostgreSQL transactional outbox

Status: accepted.

A job and publication obligation must commit atomically. PostgreSQL stores both
in one transaction; a publisher later sends the event and marks it. Direct
database-then-NATS dual writes can lose accepted jobs. The tradeoff is polling
and possible duplicate publication, handled by idempotent consumers.
