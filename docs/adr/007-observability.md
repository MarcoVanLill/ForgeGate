# ADR-007: OpenTelemetry plus Prometheus

Status: accepted.

OpenTelemetry carries trace context across HTTP, pgx, Redis, NATS, jobs, and
provider attempts. Prometheus records bounded-cardinality service and queue
metrics; Grafana presents RED and job-health views. Request/job IDs link JSON
logs. Tenant, prompt, job UUID, and model are excluded from metric labels.
