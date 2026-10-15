# ADR-003: Use NATS JetStream for jobs

Status: accepted.

JetStream provides durable subjects, explicit acknowledgements, delayed NAK,
consumer groups, and simple local operation. It fits the job workflow without a
larger streaming platform. PostgreSQL remains authoritative job state; JetStream
is delivery, not the system of record.
