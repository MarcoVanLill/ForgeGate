# Threat model

Assets include tenant and provider keys, prompts/results, usage/cost records,
routing policy, audit history, and job integrity. Trust boundaries exist at the
public gateway, dependency networks, provider egress, runtime, and control plane.

| Threat | Existing control | Residual risk / follow-up |
|---|---|---|
| Stolen API key | Hash-only storage; expiry/disable fields; TLS expected | Add self-service rotation and scopes |
| Cross-tenant job access | Tenant predicate on read/cancel | Add adversarial tests for every future endpoint |
| Prompt leakage | No prompt logging; request table stores hash | Async payload/result remain in DB; add retention/encryption |
| Secret leakage | Provider key env references and Kubernetes Secrets | Use cloud secret manager and rotation |
| Replay/duplicate job | Tenant idempotency constraint and conditional claim | Enforce idempotency retention |
| Resource exhaustion | Body/rate/budget/concurrency limits; bounded fetch | Add global stream caps and edge WAF |
| SSRF through provider URL | URLs are operator config, not request input | Restrict control-plane RBAC and egress |
| Dependency spoofing | Production TLS expected | Compose is plaintext; configure CA/mTLS |
| Audit tampering | Audit shares mutation transaction | Export to immutable audit storage |
| Supply-chain compromise | Pinned modules/images; vulnerability CI | Add SBOM, signatures, digest pins, provenance |

Never log Authorization headers, keys, complete prompts, or full provider error
bodies. Request/job IDs, tenant UUID, provider name, normalized error class,
counts, and timings are acceptable. DLQ payloads and snapshots are sensitive.

Abuse tests should cover oversized/malformed bodies, cross-tenant UUIDs, key
disable during traffic, idempotency collisions with changed payload, slow stream
readers, provider output containing control characters, and cancellation races.

The current duplicate-key behavior returns the original job even if a later
payload differs. Clients must bind keys to one operation; a future request-hash
conflict would tighten misuse detection.
