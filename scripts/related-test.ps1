$ErrorActionPreference = 'Stop'
$root = 'c:\WebSearch_GO'
$port = 8080
$envFile = Join-Path $root '.env'
if (Test-Path $envFile) {
    $match = Select-String -Path $envFile -Pattern '^\s*PORT\s*=\s*"?(\d+)"?' | Select-Object -First 1
    if ($null -ne $match) { $port = [int]$match.Matches.Groups[1].Value }
}
# LIVE=1 targets the deployed Cloud Run service instead of localhost.
$base = "http://localhost:$port"
if ($env:LIVE -eq '1') { $base = 'https://websearch-go-451921002283.asia-south1.run.app' }

$body = @{ mode = 'related_entity'; query = 'KLJ Resources Limited' }
$statusFile = Join-Path $root 'artifacts\related-test-status.txt'
$responseFile = Join-Path $root 'artifacts\related-response.html'

try {
    $response = Invoke-WebRequest -Method POST -Uri "$base/api/research?sync=1" -Body $body -TimeoutSec 1200 -UseBasicParsing
    $response.Content | Out-File -FilePath $responseFile -Encoding utf8
    "STATUS:$($response.StatusCode)" | Out-File -FilePath $statusFile -Encoding utf8
} catch {
    "ERROR:$($_.Exception.Message)" | Out-File -FilePath $statusFile -Encoding utf8
    try {
        $stream = $_.Exception.Response.GetResponseStream()
        if ($null -ne $stream) {
            $reader = New-Object System.IO.StreamReader($stream)
            $reader.ReadToEnd() | Out-File -FilePath $responseFile -Encoding utf8
        }
    } catch {
    }
}