# Validation record

This record captures the checks run against the repository on 2026-10-15. It
is evidence of functional coverage, not a production performance claim.

| Check | Result |
| --- | --- |
| `go test ./...` | Passed |
| `go vet ./...` | Passed |
| Staticcheck 0.8.2 | Passed |
| govulncheck 1.9.0 | Passed with zero reachable vulnerabilities |
| Testcontainers integration suite | Passed with PostgreSQL 16, Redis 7, and NATS 2.10 before the final dependency refresh |
| Integration-tag compile check | Passed after the final dependency refresh |
| Docker Compose configuration | Passed |
| Full Compose startup and health checks | Passed |
| PowerShell end-to-end demo | Passed, including sync, SSE, async completion, idempotency, rate limiting, and fallback |
| k6 Compose-network smoke profile | Passed all thresholds |
| Helm lint and template render | Passed with Helm 3.16.4 |
| Terraform format and validate | Passed |
| Terraform default speculative plan | Passed and proposes no infrastructure resources |

The native Windows Go installation has CGO disabled and cannot execute the
race detector. The CI workflow runs `go test -race ./...` on Linux. A local
containerized rerun was unavailable at the end of validation because the
Docker Desktop Linux engine had stopped; the same engine had already completed
the Compose, integration, Helm, and k6 checks listed above. The dependency
refresh changed no application contract, and the integration target compiles,
but the runtime integration row intentionally records when it was executed.

No latency or throughput number is presented as a benchmark. Run the profiles
in [`loadtest/`](../loadtest/) in the target environment and retain the raw k6
output before making an SLO or capacity claim.
