# Incident simulation

Run only in local or approved non-production environments. Capture timestamps,
expected/actual signals, decisions, and follow-ups. Restore each dependency
before the next drill.

## PostgreSQL interruption

Start steady traffic, stop PostgreSQL, and expect readiness failure plus API/job
errors without lost acknowledged work. Restart it and verify readiness and outbox
drain.

## Redis interruption

Stop Redis during synchronous traffic. Expect fail-closed 503 responses with
`rate_limiter_unavailable`, while accepted jobs can finish. Restore and verify
automatic recovery.

## NATS interruption

Stop NATS and submit async jobs. Jobs/outbox obligations remain committed even
though readiness is false. Restore NATS and confirm eventual publish/completion.

## Provider degradation

Use model `fake/fallback`. Expect primary safe retries, fallback metrics, and a
secondary success. Repeated failure should open the primary circuit.

## Worker crash/redelivery

Terminate a worker after delivery but before acknowledgement. Restart and confirm
the conditional claim prevents a terminal job from executing twice.

The drill succeeds only if user impact, alerts, traces, recovery, and data
invariants match observation. Restarting alone is not proof of correctness.
