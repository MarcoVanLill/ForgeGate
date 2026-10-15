# ADR-004: Accept at-least-once delivery

Status: accepted.

End-to-end exactly-once execution is not credible across PostgreSQL, NATS, and an
external provider. ForgeGate accepts redelivery and uses unique tenant
idempotency keys plus conditional job-state transitions. This favors no lost
accepted work over an unprovable exactly-once claim.
