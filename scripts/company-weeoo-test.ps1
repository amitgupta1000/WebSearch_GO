$ErrorActionPreference = 'Stop'
$root = 'c:\WebSearch_GO'
$port = 8080

$body = @{
    mode = 'company'
    query = 'KLJ Resources Limited'
    company_domain = 'kljresources.in'
    entity_confirmed = '1'
    source_profile = 'india'
}
$statusFile = Join-Path $root 'artifacts\company-weeoo-status.txt'
$responseFile = Join-Path $root 'artifacts\company-weeoo-response.html'

try {
    $response = Invoke-WebRequest -Method POST -Uri "http://localhost:$port/api/research?sync=1" -Body $body -TimeoutSec 1500 -UseBasicParsing
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