param(
    [string]$Project = "gen-lang-client-0665888431",

    [string]$Region = "asia-south1",
    [string]$Registry = "gcr.io",
    [string]$Service = "websearch-go",
    [string]$Tag = "latest"
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($Tag)) {
    $Tag = Get-Date -Format "yyyyMMdd-HHmmss"
}

$image = "$Registry/$Project/websearch-go:$Tag"

Write-Host "Building browser-enabled image: $image"
& gcloud builds submit `
    --project $Project `
    --config cloudbuild.browser.yaml `
    --substitutions "_IMAGE=$image" `
    .
if ($LASTEXITCODE -ne 0) {
    throw "Cloud Build failed."
}

Write-Host "Deploying $Service from $image"
 $browserEnvFile = Join-Path $env:TEMP "websearch-go-browser-env-$PID.yaml"
try {
    $envContent = Get-Content .\deploy\cloud-run.env.yaml -Raw
    $envContent = $envContent -replace '(?m)^ENABLE_BROWSER_FALLBACK:.*$', 'ENABLE_BROWSER_FALLBACK: "true"'
    $envContent = $envContent -replace '(?m)^ALLOW_BROWSER_TOGGLE:.*$', 'ALLOW_BROWSER_TOGGLE: "true"'
    Set-Content -Path $browserEnvFile -Value $envContent -NoNewline

    & gcloud run deploy $Service `
        --project $Project `
        --image $image `
        --region $Region `
        --env-vars-file $browserEnvFile `
        --set-secrets "GOOGLE_API_KEY=gemini_api_key:latest,SERPER_API_KEY=serper_api_key:latest,ROCKETREACH_API_KEY=rocketreach_api_key:latest,REDIS_URL=redis_url_alt:latest"
    if ($LASTEXITCODE -ne 0) {
        throw "Cloud Run deployment failed."
    }
}
finally {
    if (Test-Path $browserEnvFile) {
        Remove-Item -LiteralPath $browserEnvFile -Force
    }
}

Write-Host "Deployment complete. Image: $image"
