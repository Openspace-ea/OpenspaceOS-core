# Openspace OS Core perf baseline (T6.6) - concurrent clients via Start-Job + HttpClient
param(
  [string]$CoreUrl  = "http://localhost:8080",
  [int]   $Clients  = 10,
  [int]   $Batches  = 20,
  [int]   $Frames   = 10
)

$ErrorActionPreference = "Stop"

# ---- login / create client (curl, proven) ----
$loginJson = @{ username = "admin"; password = "admin123" } | ConvertTo-Json -Compress
$tmp = [System.IO.Path]::Combine([System.IO.Path]::GetTempPath(), "bench-login.json")
[System.IO.File]::WriteAllText($tmp, $loginJson)
$login = curl.exe -s -X POST "$CoreUrl/api/v1/auth/login" -H "Content-Type: application/json" --data "@$tmp"
[System.IO.File]::Delete($tmp)
$token = ($login | ConvertFrom-Json).token
if (-not $token) { Write-Host "login failed"; exit 1 }

$clientJson = @{ name = "bench-client"; communityId = "community-001" } | ConvertTo-Json -Compress
[System.IO.File]::WriteAllText($tmp, $clientJson)
$created = curl.exe -s -X POST "$CoreUrl/api/v1/clients" -H "Authorization: Bearer $token" -H "Content-Type: application/json" --data "@$tmp"
[System.IO.File]::Delete($tmp)
$apiKey = ($created | ConvertFrom-Json).apiKey
if (-not $apiKey) { Write-Host "client create failed: $created"; exit 1 }
Write-Host "perf client created (apiKey=$($apiKey.Substring(0,8))...)"

# ---- payload ----
$frameArr = @()
for ($f = 0; $f -lt $Frames; $f++) {
  $frameArr += @{ satelliteId = "bench-sat-$f"; parameters = @{ voltage = (3.0 + $f * 0.1); temp = 45.2 }; quality = "good" }
}
$payloadJson = (@{ frames = $frameArr } | ConvertTo-Json -Compress -Depth 6)

$url = "$CoreUrl/api/v1/telemetry/ingest"

# ---- concurrent clients: one Start-Job per client ----
$scriptBlock = {
  param($key, $pay, $u, $n)
  Add-Type -AssemblyName System.Net.Http -ErrorAction SilentlyContinue
  $client = [System.Net.Http.HttpClient]::new()
  $client.Timeout = [TimeSpan]::FromSeconds(30)
  $client.DefaultRequestHeaders.Authorization = [System.Net.Http.Headers.AuthenticationHeaderValue]::new("Bearer", $key)
  $lat = [System.Collections.Generic.List[long]]::new()
  $ok = 0; $total = 0
  for ($i = 0; $i -lt $n; $i++) {
    $content = [System.Net.Http.StringContent]::new($pay, [System.Text.Encoding]::UTF8, "application/json")
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    try {
      $resp = $client.PostAsync($u, $content).GetAwaiter().GetResult()
      $code = [int]$resp.StatusCode
      $resp.Dispose()
      if ($code -eq 202) { $ok++ }
    } catch { }
    $sw.Stop()
    $lat.Add([long]$sw.Elapsed.TotalMilliseconds)
    $total++
  }
  $client.Dispose()
  @{ ok = $ok; total = $total; lat = $lat.ToArray() } | ConvertTo-Json -Compress
}

$jobs = @()
for ($c = 0; $c -lt $Clients; $c++) {
  $jobs += Start-Job -ScriptBlock $scriptBlock -ArgumentList $apiKey, $payloadJson, $url, $Batches
}
$sw = [System.Diagnostics.Stopwatch]::StartNew()
$jsonResults = $jobs | Wait-Job | Receive-Job
$jobs | Remove-Job
$sw.Stop()
$elapsedSec = $sw.Elapsed.TotalSeconds

# ---- aggregate ----
$all = [System.Collections.Generic.List[long]]::new()
$totOk = 0; $totTotal = 0
foreach ($one in $jsonResults) {
  $r = $one | ConvertFrom-Json
  $totOk += [int]$r.ok; $totTotal += [int]$r.total
  foreach ($l in $r.lat) { $all.Add([long]$l) }
}
$sorted = @($all.ToArray() | Sort-Object)
$n = $sorted.Count
function Pct([double]$q) { if ($n -eq 0) { return 0.0 }; return [double]$sorted[[int][Math]::Ceiling($q * $n) - 1] }
$passPct = if ($totTotal -gt 0) { [Math]::Round(100.0 * $totOk / $totTotal, 2) } else { 0.0 }
$framesDone = $totTotal * $Frames
$throughput = if ($elapsedSec -gt 0) { [Math]::Round($framesDone / $elapsedSec, 1) } else { 0 }

Write-Host ""
Write-Host "==================== T6.6 PERF BASELINE ===================="
Write-Host "  concurrent clients : $Clients   (frames/req=$Frames, batch/client=$Batches)"
Write-Host "  total requests     : $totTotal"
Write-Host "  passed (HTTP 202)  : $totOk"
Write-Host "  PASS %             : $passPct %"
Write-Host "  total frames       : $framesDone"
Write-Host "  elapsed (s)        : $([Math]::Round($elapsedSec,3))"
Write-Host "  throughput (fr/s)  : $throughput"
Write-Host "  latency P50 (ms)   : $([Math]::Round((Pct 0.50),2))"
Write-Host "  latency P95 (ms)   : $([Math]::Round((Pct 0.95),2))"
Write-Host "  latency P99 (ms)   : $([Math]::Round((Pct 0.99),2))"
Write-Host "==========================================================="