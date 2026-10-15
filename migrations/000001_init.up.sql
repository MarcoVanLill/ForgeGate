CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tenants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    requests_per_minute integer NOT NULL DEFAULT 60 CHECK (requests_per_minute > 0),
    monthly_request_budget bigint NOT NULL DEFAULT 100000 CHECK (monthly_request_budget >= 0),
    monthly_cost_budget_micros bigint NOT NULL DEFAULT 0 CHECK (monthly_cost_budget_micros >= 0),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key_hash bytea NOT NULL UNIQUE,
    key_prefix text NOT NULL,
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz
);
CREATE INDEX api_keys_tenant_idx ON api_keys(tenant_id);

CREATE TABLE provider_configs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('fake', 'gemini', 'openai-compatible')),
    base_url text,
    api_key_env text,
    model_prefix text NOT NULL DEFAULT '',
    priority integer NOT NULL DEFAULT 100,
    timeout_ms integer NOT NULL DEFAULT 30000 CHECK (timeout_ms > 0),
    max_retries integer NOT NULL DEFAULT 2 CHECK (max_retries BETWEEN 0 AND 5),
    max_concurrency integer NOT NULL DEFAULT 16 CHECK (max_concurrency > 0),
    enabled boolean NOT NULL DEFAULT true,
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, name)
);
CREATE INDEX provider_configs_route_idx ON provider_configs(tenant_id, enabled, priority);

CREATE TABLE routing_policies (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name text NOT NULL,
    model_pattern text NOT NULL DEFAULT '*',
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, name)
);

CREATE TABLE request_records (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    request_id text NOT NULL,
    model text NOT NULL,
    input_hash bytea NOT NULL,
    selected_provider text,
    outcome text NOT NULL,
    status_code integer NOT NULL,
    latency_ms bigint NOT NULL,
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX request_records_tenant_time_idx ON request_records(tenant_id, created_at DESC);

CREATE TABLE usage_records (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_record_id uuid NOT NULL REFERENCES request_records(id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    input_tokens integer NOT NULL DEFAULT 0,
    output_tokens integer NOT NULL DEFAULT 0,
    total_tokens integer NOT NULL DEFAULT 0,
    cost_micros bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX usage_records_tenant_time_idx ON usage_records(tenant_id, created_at DESC);

CREATE TABLE tenant_usage_monthly (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    month date NOT NULL,
    request_count bigint NOT NULL DEFAULT 0,
    cost_micros bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(tenant_id, month)
);

CREATE TABLE jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    idempotency_key text NOT NULL,
    status text NOT NULL CHECK (status IN ('queued','running','retrying','completed','failed','cancelled')),
    request jsonb NOT NULL,
    result jsonb,
    selected_provider text,
    error_code text,
    error_message text,
    attempts integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 5,
    cancellation_requested boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    UNIQUE(tenant_id, idempotency_key)
);
CREATE INDEX jobs_tenant_time_idx ON jobs(tenant_id, created_at DESC);
CREATE INDEX jobs_status_idx ON jobs(status, updated_at);

CREATE TABLE audit_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid REFERENCES tenants(id),
    actor text NOT NULL,
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL,
    request_id text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_tenant_time_idx ON audit_events(tenant_id, created_at DESC);

CREATE TABLE outbox_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    published_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_unpublished_idx ON outbox_events(next_attempt_at, created_at) WHERE published_at IS NULL;

INSERT INTO tenants (id, name, requests_per_minute, monthly_request_budget, monthly_cost_budget_micros)
VALUES ('11111111-1111-1111-1111-111111111111', 'local-demo', 10, 10000, 100000000)
ON CONFLICT (id) DO NOTHING;

-- Local-only key: fg_demo_local_only. Production keys are created out-of-band and only their SHA-256 hashes are stored.
INSERT INTO api_keys (id, tenant_id, key_hash, key_prefix, name)
VALUES (
    '22222222-2222-2222-2222-222222222222',
    '11111111-1111-1111-1111-111111111111',
    decode('12ae320dc0223da15192c6f1887ac489b48d42669a401debc7f19a325557cb88', 'hex'),
    'fg_demo_',
    'local-demo-key'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO provider_configs (id, tenant_id, name, kind, priority, timeout_ms, max_retries, max_concurrency, settings)
VALUES
    ('33333333-3333-3333-3333-333333333331', '11111111-1111-1111-1111-111111111111', 'fake-primary', 'fake', 10, 5000, 1, 16, '{"fail_models":{"fake/fallback":"unavailable"}}'),
    ('33333333-3333-3333-3333-333333333332', '11111111-1111-1111-1111-111111111111', 'fake-secondary', 'fake', 20, 5000, 1, 16, '{}')
ON CONFLICT (id) DO NOTHING;

INSERT INTO routing_policies (id, tenant_id, name, model_pattern)
VALUES ('44444444-4444-4444-4444-444444444444', '11111111-1111-1111-1111-111111111111', 'default-priority-fallback', '*')
ON CONFLICT (id) DO NOTHING;

INSERT INTO audit_events (tenant_id, actor, action, target_type, target_id, metadata)
SELECT '11111111-1111-1111-1111-111111111111', 'migration:000001', 'provider.config_seeded', 'provider', 'fake-primary', '{"local_only":true}'::jsonb
WHERE NOT EXISTS (SELECT 1 FROM audit_events WHERE actor='migration:000001' AND action='provider.config_seeded' AND target_id='fake-primary');
