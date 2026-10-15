# Optional AWS foundation

The module defaults to `enable_deployment = false`, so a normal `terraform plan`
creates no resources and does not require credentials. When explicitly enabled it
creates a VPC, two-AZ subnet layout, encrypted RDS PostgreSQL, encrypted
ElastiCache Redis, an ECS cluster, and immutable/scanned ECR repositories.

It deliberately stops short of creating ECS services or a NATS cluster: those
need environment-specific decisions about ingress, DNS, certificates, JetStream
storage/replication, and secret management. The Helm chart is the complete
deployment path in this repository; this module is a safe AWS foundation.

```bash
terraform init -backend=false
terraform plan
# Explicit provisioning requires credentials and a strong password:
TF_VAR_enable_deployment=true TF_VAR_db_password='...' terraform plan
```
