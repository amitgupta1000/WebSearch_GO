$ErrorActionPreference = 'Stop'

$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$goCommand = Get-Command go -ErrorAction SilentlyContinue
$go = if ($null -ne $goCommand) { $goCommand.Source } else { $null }

if ([string]::IsNullOrWhiteSpace($go)) {
    $knownGoPaths = @(
        (Join-Path ${env:ProgramFiles} 'Go\bin\go.exe'),
        (Join-Path ${env:LOCALAPPDATA} 'Programs\Go\bin\go.exe')
    )
    $go = $knownGoPaths | Where-Object { Test-Path $_ } | Select-Object -First 1
}

if ([string]::IsNullOrWhiteSpace($go)) {
    throw 'Go was not found. Add Go to PATH or install it from https://go.dev/dl/.'
}

$server = $null

function Get-BackendFingerprint {
    $files = Get-ChildItem -Path (Join-Path $root 'cmd'), (Join-Path $root 'internal') -Recurse -File |
        Where-Object { $_.Extension -in @('.go', '.html') } |
        Sort-Object FullName

    return (($files | ForEach-Object {
        "$($_.FullName)|$($_.Length)|$($_.LastWriteTimeUtc.Ticks)"
    }) -join "`n")
}

function Stop-Server {
    if ($null -ne $script:server -and !$script:server.HasExited) {
        Write-Host 'Stopping server...'
        & taskkill.exe /PID $script:server.Id /T /F | Out-Null
        $script:server.WaitForExit()
    }
    $script:server = $null
}

function Start-Server {
    Write-Host 'Starting go run ./cmd/server...'
    $script:server = Start-Process -FilePath $go `
        -ArgumentList @('run', './cmd/server') `
        -WorkingDirectory $root `
        -NoNewWindow `
        -PassThru
}

try {
    $fingerprint = Get-BackendFingerprint
    Start-Server

    while ($true) {
        Start-Sleep -Milliseconds 750
        $newFingerprint = Get-BackendFingerprint

        if ($newFingerprint -ne $fingerprint) {
            $fingerprint = $newFingerprint
            Stop-Server
            Start-Sleep -Milliseconds 250
            Start-Server
        }
    }
}
finally {
    Stop-Server
}
