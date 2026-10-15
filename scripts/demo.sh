#!/usr/bin/env sh
set -eu

base_url="${FORGEGATE_URL:-http://localhost:8080}"
api_key="${FORGEGATE_API_KEY:-fg_demo_local_only}"
rate_api_key="${FORGEGATE_RATE_LIMIT_API_KEY:-fg_rate_limit_local_only}"
auth="Authorization: Bearer ${api_key}"

echo "1. Synchronous response"
curl -fsS -H "$auth" -H 'Content-Type: application/json' -d '{"model":"fake/echo","input":"hello from the ForgeGate demo"}' "$base_url/v1/responses"
echo

echo "2. Streaming response"
curl -fsSN -H "$auth" -H 'Content-Type: application/json' -d '{"model":"fake/echo","input":"stream this response","stream":true}' "$base_url/v1/responses"

echo "3. Asynchronous job"
job_json=$(curl -fsS -H "$auth" -H 'Content-Type: application/json' -H 'Idempotency-Key: demo-job-1' -d '{"model":"fake/echo","input":"run this asynchronously"}' "$base_url/v1/jobs")
echo "$job_json"
job_id=$(printf '%s' "$job_json" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

echo "4. Fetch completed result"
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  result=$(curl -fsS -H "$auth" "$base_url/v1/jobs/$job_id")
  echo "$result"
  printf '%s' "$result" | grep -q '"status":"completed"' && break
  sleep 1
done

echo "5. Duplicate idempotency request"
curl -fsS -H "$auth" -H 'Content-Type: application/json' -H 'Idempotency-Key: demo-job-1' -d '{"model":"fake/echo","input":"run this asynchronously"}' "$base_url/v1/jobs"
echo

echo "6. Rate-limit rejection (the dedicated tenant allows two requests/minute)"
for attempt in 1 2 3 4; do
  curl -sS -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer ${rate_api_key}" -H 'Content-Type: application/json' -d '{"model":"fake/echo","input":"rate limit probe"}' "$base_url/v1/responses"
done

echo "7. Provider fallback (provider should be fake-secondary)"
curl -fsS -H "$auth" -H 'Content-Type: application/json' -d '{"model":"fake/fallback","input":"exercise fallback"}' "$base_url/v1/responses"
echo
