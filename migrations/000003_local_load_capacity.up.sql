-- The primary local tenant is used by k6. Rate-limit behavior is isolated in
-- the dedicated two-request tenant from migration 000002.
UPDATE tenants
SET requests_per_minute = 10000, updated_at = now()
WHERE id = '11111111-1111-1111-1111-111111111111';
