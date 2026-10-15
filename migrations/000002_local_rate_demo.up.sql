-- A second local-only tenant lets the demo exhaust a small limit without
-- consuming the main tenant's quota before the fallback example.
INSERT INTO tenants (id, name, requests_per_minute, monthly_request_budget, monthly_cost_budget_micros)
VALUES ('55555555-5555-5555-5555-555555555555', 'local-rate-limit-demo', 2, 10000, 100000000)
ON CONFLICT (id) DO NOTHING;

-- Local-only key: fg_rate_limit_local_only.
INSERT INTO api_keys (id, tenant_id, key_hash, key_prefix, name)
VALUES (
    '66666666-6666-6666-6666-666666666666',
    '55555555-5555-5555-5555-555555555555',
    decode('f17a758ad1f69e6dfe92b63ff8482de4cd95ec17d27210c225247079aabb0cf5', 'hex'),
    'fg_rate_',
    'local-rate-limit-demo-key'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO provider_configs (id, tenant_id, name, kind, priority, timeout_ms, max_retries, max_concurrency, settings)
VALUES
    ('77777777-7777-7777-7777-777777777771', '55555555-5555-5555-5555-555555555555', 'fake-primary', 'fake', 10, 5000, 1, 16, '{}'),
    ('77777777-7777-7777-7777-777777777772', '55555555-5555-5555-5555-555555555555', 'fake-secondary', 'fake', 20, 5000, 1, 16, '{}')
ON CONFLICT (id) DO NOTHING;
