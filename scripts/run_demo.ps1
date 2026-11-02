[CmdletBinding()]
param(
    [switch]$NoBrowser,
    [switch]$VerifyOnly
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$runtimeRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("pulse-demo-" + [guid]::NewGuid().ToString('N'))
$serverExecutable = Join-Path $runtimeRoot 'pulse-server.exe'
$agentExecutable = Join-Path $runtimeRoot 'pulse-agent.exe'
$serverOutput = Join-Path $runtimeRoot 'server.stdout.log'
$serverError = Join-Path $runtimeRoot 'server.stderr.log'
$agentOutput = Join-Path $runtimeRoot 'agent.stdout.log'
$agentError = Join-Path $runtimeRoot 'agent.stderr.log'
$dashboardURL = 'http://127.0.0.1:8080'
$serverProcess = $null
$agentProcess = $null
$agentRun = 0
$applicationStarted = $false
$demoStarted = $false
$completed = $false

$dockerCommand = Get-Command docker -ErrorAction SilentlyContinue
$dockerPath = if ($dockerCommand) {
    $dockerCommand.Source
} else {
    Join-Path $env:LOCALAPPDATA 'Programs\DockerDesktop\resources\bin\docker.exe'
}

function Invoke-Docker {
    param([Parameter(ValueFromRemainingArguments)][string[]]$Arguments)

    & $dockerPath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Docker command '$($Arguments -join ' ')' failed with exit code $LASTEXITCODE."
    }
}

function Test-PortInUse {
    param([int]$Port)

    $client = [System.Net.Sockets.TcpClient]::new()
    try {
        $connection = $client.ConnectAsync('127.0.0.1', $Port)
        return $connection.Wait(300) -and $client.Connected
    } catch {
        return $false
    } finally {
        $client.Dispose()
    }
}

function Wait-ForHealth {
    param([System.Diagnostics.Process]$Process)

    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        if ($Process.HasExited) {
            throw "Pulse server exited during startup. See $serverError"
        }
        try {
            $health = Invoke-RestMethod "$dashboardURL/healthz" -TimeoutSec 1
            if ($health.status -eq 'ok') { return }
        } catch {
            # A connection failure is expected while the server is starting.
        }
        Start-Sleep -Milliseconds 500
    }
    throw "Pulse server did not become healthy within 20 seconds. See $serverError"
}

function Wait-ForTelemetry {
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        if ($agentProcess.HasExited) {
            throw "Pulse agent exited during startup. See $agentError"
        }
        try {
            $overview = Invoke-RestMethod "$dashboardURL/api/v1/containers" -TimeoutSec 2
            $demoContainer = $overview.containers |
                Where-Object { $_.container_name -eq 'pulse-demo-monitored-container-1' -and $_.freshness -eq 'current' } |
                Select-Object -First 1
            if ($null -ne $demoContainer) { return $demoContainer }
        } catch {
            # The first collection and ingestion cycle may still be running.
        }
        Start-Sleep -Seconds 1
    }
    throw "The demo container did not produce current telemetry within 30 seconds. See $agentError"
}

function Start-PulseAgent {
    $script:agentRun++
    $script:agentOutput = Join-Path $runtimeRoot "agent-$($script:agentRun).stdout.log"
    $script:agentError = Join-Path $runtimeRoot "agent-$($script:agentRun).stderr.log"
    return Start-Process -FilePath $agentExecutable -WorkingDirectory $repositoryRoot -WindowStyle Hidden -RedirectStandardOutput $script:agentOutput -RedirectStandardError $script:agentError -PassThru
}

function Start-CPUScenario {
    $command = "if mkdir /tmp/pulse-cpu.lock 2>/dev/null; then trap 'rmdir /tmp/pulse-cpu.lock' EXIT; timeout 15 sh -c 'while :; do :; done'; else exit 23; fi"
    Invoke-Docker -Arguments @('exec', '-d', 'pulse-demo-monitored-container-1', 'sh', '-c', $command)
    Write-Host 'CPU pressure started for 15 seconds.' -ForegroundColor Cyan
    Write-Host 'Watch CPU rise above 80%, then pending -> firing -> recovering -> resolved.'
}

function Start-MemoryScenario {
    $command = 'if mkdir /tmp/pulse-memory.lock 2>/dev/null; then trap ''rm -f /tmp/pulse-memory.bin; rmdir /tmp/pulse-memory.lock'' EXIT TERM INT; dd if=/dev/zero of=/tmp/pulse-memory.bin bs=1M count=36 2>/dev/null; sleep 20; else exit 23; fi'
    Invoke-Docker -Arguments @('exec', '-d', 'pulse-demo-monitored-container-1', 'sh', '-c', $command)
    Write-Host 'Memory pressure started for 20 seconds (36 MiB inside the 64 MiB limit).' -ForegroundColor Cyan
    Write-Host 'Watch memory rise above the demo threshold, then recover automatically.'
}

function Start-NetworkScenario {
    $command = 'if mkdir /tmp/pulse-network.lock 2>/dev/null; then trap ''rmdir /tmp/pulse-network.lock'' EXIT TERM INT; dd if=/dev/zero bs=1M count=20 2>/dev/null | nc -w 15 traffic-target 8080 >/dev/null; else exit 23; fi'
    Invoke-Docker -Arguments @('exec', '-d', 'pulse-demo-monitored-container-1', 'sh', '-c', $command)
    Write-Host 'Local network transfer started (20 MiB maximum, no internet traffic).' -ForegroundColor Cyan
    Write-Host 'Watch the Network I/O chart show received throughput.'
}

function Stop-MonitoredContainer {
    Invoke-Docker compose -f deploy/pulse-demo.yaml stop monitored-container
    Write-Host 'The monitored container is stopped.' -ForegroundColor Cyan
    Write-Host 'Watch availability move from pending to firing.'
}

function Start-MonitoredContainer {
    Invoke-Docker compose -f deploy/pulse-demo.yaml start monitored-container
    Write-Host 'The monitored container is running again.' -ForegroundColor Cyan
    Write-Host 'Watch the incident move through recovering to resolved.'
}

function Interrupt-AgentScenario {
    if (-not $script:agentProcess -or $script:agentProcess.HasExited) {
        throw 'The Pulse agent is not running.'
    }
    Stop-Process -Id $script:agentProcess.Id
    $script:agentProcess.WaitForExit(5000) | Out-Null
    Write-Host 'Agent stopped for 25 seconds. Watch telemetry become stale and the agent incident fire.' -ForegroundColor Cyan
    Start-Sleep -Seconds 25
    $script:agentProcess = Start-PulseAgent
    $null = Wait-ForTelemetry
    Write-Host 'Agent reconnected. Queued delivery and incident recovery are now visible.' -ForegroundColor Green
}

function Show-DemoMenu {
    while ($true) {
        Write-Host ''
        Write-Host 'Pulse demo controls' -ForegroundColor Green
        Write-Host '  [1] CPU pressure (15 seconds)'
        Write-Host '  [2] Memory pressure (20 seconds)'
        Write-Host '  [3] Local network traffic (20 MiB maximum)'
        Write-Host '  [4] Stop monitored container'
        Write-Host '  [5] Restart monitored container'
        Write-Host '  [6] Interrupt agent and reconnect automatically'
        Write-Host '  [Q] Stop and clean up'
        $choice = (Read-Host 'Choose an action').Trim().ToUpperInvariant()
        if ($choice -eq 'Q') { return }
        try {
            switch ($choice) {
                '1' { Start-CPUScenario }
                '2' { Start-MemoryScenario }
                '3' { Start-NetworkScenario }
                '4' { Stop-MonitoredContainer }
                '5' { Start-MonitoredContainer }
                '6' { Interrupt-AgentScenario }
                default { Write-Warning 'Choose 1-6 or Q.' }
            }
        } catch {
            Write-Warning "Demo action failed without changing Pulse data: $($_.Exception.Message)"
        }
    }
}

if (-not (Test-Path -LiteralPath $dockerPath)) {
    throw 'Docker CLI was not found. Start or install Docker Desktop and try again.'
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go was not found on PATH. Install the Go version listed in go.mod and try again.'
}

$environmentNames = @(
    'PULSE_AGENT_ID', 'PULSE_AGENT_INTERVAL', 'PULSE_AGENT_SPOOL_DIR',
    'PULSE_ALERT_MEMORY_PERCENT',
    'PULSE_ALERT_RESOURCE_SUSTAIN', 'PULSE_ALERT_AVAILABILITY_SUSTAIN',
    'PULSE_ALERT_RECOVERY', 'PULSE_ALERT_AGENT_STALE_AFTER',
    'PULSE_ALERT_CONTAINER_STALE_AFTER', 'PULSE_ALERT_EVALUATION_INTERVAL'
)
$previousEnvironment = @{}
$environmentNames | ForEach-Object { $previousEnvironment[$_] = [Environment]::GetEnvironmentVariable($_) }

Push-Location $repositoryRoot
try {
    $applicationContainers = & $dockerPath compose -f compose.yaml ps --all --quiet
    $demoContainers = & $dockerPath compose -f deploy/pulse-demo.yaml ps --all --quiet
    if ($applicationContainers -or $demoContainers) {
        throw 'Pulse Compose containers already exist. Stop them first so this launcher does not interfere with another session.'
    }
    if (Test-PortInUse -Port 8080) {
        throw 'Port 8080 is already in use. Pulse will not stop or replace the process using it.'
    }
    if (Test-PortInUse -Port 5433) {
        throw 'Port 5433 is already in use. Pulse will not stop or replace the process using it.'
    }

    Write-Host 'Validating the resource-limited Pulse environment...'
    Invoke-Docker compose -f compose.yaml config --quiet
    Invoke-Docker compose -f deploy/pulse-demo.yaml config --quiet

    New-Item -ItemType Directory -Path $runtimeRoot | Out-Null
    Write-Host 'Building Pulse server and agent...'
    & go build -o $serverExecutable ./cmd/pulse-server
    if ($LASTEXITCODE -ne 0) { throw 'Could not build the Pulse server.' }
    & go build -o $agentExecutable ./cmd/pulse-agent
    if ($LASTEXITCODE -ne 0) { throw 'Could not build the Pulse agent.' }

    Write-Host 'Starting PostgreSQL and the constrained demo container...'
    $applicationStarted = $true
    Invoke-Docker compose -f compose.yaml up -d --wait postgres
    $demoStarted = $true
    Invoke-Docker compose -f deploy/pulse-demo.yaml up -d --wait

    $env:PULSE_ALERT_RESOURCE_SUSTAIN = '8s'
    $env:PULSE_ALERT_MEMORY_PERCENT = '55'
    $env:PULSE_ALERT_AVAILABILITY_SUSTAIN = '5s'
    $env:PULSE_ALERT_RECOVERY = '5s'
    $env:PULSE_ALERT_AGENT_STALE_AFTER = '15s'
    $env:PULSE_ALERT_CONTAINER_STALE_AFTER = '10s'
    $env:PULSE_ALERT_EVALUATION_INTERVAL = '1s'
    $serverProcess = Start-Process -FilePath $serverExecutable -WorkingDirectory $repositoryRoot -WindowStyle Hidden -RedirectStandardOutput $serverOutput -RedirectStandardError $serverError -PassThru
    Wait-ForHealth -Process $serverProcess

    $env:PULSE_AGENT_ID = 'pulse-demo-local'
    $env:PULSE_AGENT_INTERVAL = '2s'
    $env:PULSE_AGENT_SPOOL_DIR = Join-Path $env:LOCALAPPDATA 'Pulse\demo-spool'
    $agentProcess = Start-PulseAgent
    $container = Wait-ForTelemetry

    Write-Host ''
    Write-Host 'Pulse is running.' -ForegroundColor Green
    Write-Host "Dashboard: $dashboardURL"
    Write-Host "Container: $($container.container_name) ($($container.container_id.Substring(0, 12)))"
    Write-Host 'The monitored workload is limited to 0.25 CPU, 64 MiB memory, and 32 processes.'
    Write-Host 'The unmonitored local traffic target is limited to 0.10 CPU, 16 MiB, and 16 processes.'
    Write-Host 'Closing this launcher stops its processes and containers but preserves PostgreSQL data.'

    if (-not $NoBrowser -and -not $VerifyOnly) {
        Start-Process $dashboardURL
    }
    $completed = $true
    if ($VerifyOnly) {
        Write-Host 'One-command demo startup verification passed.' -ForegroundColor Green
    } else {
        Show-DemoMenu
    }
} finally {
    Write-Host 'Stopping Pulse demo resources...'
    if ($agentProcess -and -not $agentProcess.HasExited) {
        Stop-Process -Id $agentProcess.Id -ErrorAction SilentlyContinue
        $agentProcess.WaitForExit(5000) | Out-Null
    }
    if ($serverProcess -and -not $serverProcess.HasExited) {
        Stop-Process -Id $serverProcess.Id -ErrorAction SilentlyContinue
        $serverProcess.WaitForExit(5000) | Out-Null
    }
    if ($demoStarted) {
        try { Invoke-Docker compose -f deploy/pulse-demo.yaml down --remove-orphans } catch { Write-Warning $_ }
    }
    if ($applicationStarted) {
        try { Invoke-Docker compose -f compose.yaml down --remove-orphans } catch { Write-Warning $_ }
    }
    foreach ($name in $environmentNames) {
        [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name])
    }
    Pop-Location

    if ($completed) {
        $resolvedRuntime = [System.IO.Path]::GetFullPath($runtimeRoot)
        $resolvedTemp = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
        if ($resolvedRuntime.StartsWith($resolvedTemp, [System.StringComparison]::OrdinalIgnoreCase) -and
            [System.IO.Path]::GetFileName($resolvedRuntime).StartsWith('pulse-demo-', [System.StringComparison]::OrdinalIgnoreCase)) {
            Remove-Item -LiteralPath $resolvedRuntime -Recurse -Force -ErrorAction SilentlyContinue
        }
    } elseif (Test-Path -LiteralPath $runtimeRoot) {
        Write-Warning "Startup logs were retained at $runtimeRoot"
    }
}
