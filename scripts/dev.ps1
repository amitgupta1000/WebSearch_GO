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

# The server exits immediately when it cannot bind its port, and that one fatal
# line is easy to miss when the watcher restarts in the background, so the port
# is resolved and cleared around every restart.
function Get-ConfiguredPort {
    $fromEnv = [Environment]::GetEnvironmentVariable('PORT')
    if (-not [string]::IsNullOrWhiteSpace($fromEnv)) {
        return [int]$fromEnv
    }
    $envFile = Join-Path $root '.env'
    if (Test-Path $envFile) {
        $match = Select-String -Path $envFile -Pattern '^\s*PORT\s*=\s*"?(\d+)"?' | Select-Object -First 1
        if ($null -ne $match) {
            return [int]$match.Matches.Groups[1].Value
        }
    }
    return 8080
}

$port = Get-ConfiguredPort

function Get-PortOwners {
    param([int]$Port)
    try {
        return @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction Stop |
            Select-Object -ExpandProperty OwningProcess -Unique)
    } catch {
        return @()
    }
}

function Clear-Port {
    param([int]$Port)
    foreach ($owner in Get-PortOwners -Port $Port) {
        $process = Get-Process -Id $owner -ErrorAction SilentlyContinue
        if ($null -eq $process) {
            continue
        }
        if ($process.ProcessName -notmatch 'wsgo|WebSearch|websearch|^go$') {
            Write-Host "[WARN] port $Port is held by $($process.ProcessName) (PID $owner). The server will exit immediately: stop it or set PORT to a free port."
            continue
        }
        Write-Host "Port $Port is still held by a previous server ($($process.ProcessName), PID $owner); stopping it first."
        & taskkill.exe /PID $owner /T /F | Out-Null
    }
    for ($attempt = 0; $attempt -lt 20; $attempt++) {
        if ((Get-PortOwners -Port $Port).Count -eq 0) {
            return
        }
        Start-Sleep -Milliseconds 250
    }
    Write-Host "[WARN] port $Port is still in use after 5s; the server may fail to start."
}

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
    Clear-Port -Port $port
    Write-Host "Starting go run ./cmd/server (port $port)..."
    $script:server = Start-Process -FilePath $go `
        -ArgumentList @('run', './cmd/server') `
        -WorkingDirectory $root `
        -NoNewWindow `
        -PassThru
    Start-Sleep -Milliseconds 1500
    if ($script:server.HasExited) {
        Write-Host "[ERROR] the server exited immediately (exit code $($script:server.ExitCode)). The reason is printed above this line."
    }
}

$lastRestart = [DateTime]::UtcNow

try {
    $fingerprint = Get-BackendFingerprint
    Start-Server

    while ($true) {
        Start-Sleep -Milliseconds 750

        if ($script:server.HasExited) {
            # A dead server must come back on its own: the usual cause is a port
            # collision, and Clear-Port resolves that on the next start.
            if (([DateTime]::UtcNow - $lastRestart).TotalSeconds -gt 5) {
                $lastRestart = [DateTime]::UtcNow
                Write-Host "[WARN] the server is not running (exit code $($script:server.ExitCode)); restarting..."
                Start-Server
            }
            continue
        }

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
