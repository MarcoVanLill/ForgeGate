$ErrorActionPreference = 'Stop'
$baseUrl = if ($env:FORGEGATE_URL) { $env:FORGEGATE_URL } else { 'http://localhost:8080' }
$apiKey = if ($env:FORGEGATE_API_KEY) { $env:FORGEGATE_API_KEY } else { 'fg_demo_local_only' }
$rateApiKey = if ($env:FORGEGATE_RATE_LIMIT_API_KEY) { $env:FORGEGATE_RATE_LIMIT_API_KEY } else { 'fg_rate_limit_local_only' }
$headers = @{ Authorization = "Bearer $apiKey" }

Write-Host '1. Synchronous response'
Invoke-RestMethod -Method Post -Uri "$baseUrl/v1/responses" -Headers $headers -ContentType 'application/json' -Body '{"model":"fake/echo","input":"hello from the ForgeGate demo"}' | ConvertTo-Json -Depth 10

Write-Host '2. Streaming response'
curl.exe -fsSN -H "Authorization: Bearer $apiKey" -H 'Content-Type: application/json' -d '{"model":"fake/echo","input":"stream this response","stream":true}' "$baseUrl/v1/responses"

Write-Host '3. Asynchronous job'
$job = Invoke-RestMethod -Method Post -Uri "$baseUrl/v1/jobs" -Headers ($headers + @{ 'Idempotency-Key' = 'demo-job-1' }) -ContentType 'application/json' -Body '{"model":"fake/echo","input":"run this asynchronously"}'
$job | ConvertTo-Json -Depth 10

Write-Host '4. Fetch completed result'
for ($attempt = 0; $attempt -lt 10; $attempt++) {
    $result = Invoke-RestMethod -Uri "$baseUrl/v1/jobs/$($job.id)" -Headers $headers
    $result | ConvertTo-Json -Depth 10
    if ($result.status -eq 'completed') { break }
    Start-Sleep -Seconds 1
}

Write-Host '5. Duplicate idempotency request'
Invoke-RestMethod -Method Post -Uri "$baseUrl/v1/jobs" -Headers ($headers + @{ 'Idempotency-Key' = 'demo-job-1' }) -ContentType 'application/json' -Body '{"model":"fake/echo","input":"run this asynchronously"}' | ConvertTo-Json -Depth 10

Write-Host '6. Rate-limit rejection (the dedicated tenant allows two requests/minute)'
1..4 | ForEach-Object { curl.exe -sS -o NUL -w "%{http_code}`n" -H "Authorization: Bearer $rateApiKey" -H 'Content-Type: application/json' -d '{"model":"fake/echo","input":"rate limit probe"}' "$baseUrl/v1/responses" }

Write-Host '7. Provider fallback (provider should be fake-secondary)'
Invoke-RestMethod -Method Post -Uri "$baseUrl/v1/responses" -Headers $headers -ContentType 'application/json' -Body '{"model":"fake/fallback","input":"exercise fallback"}' | ConvertTo-Json -Depth 10
