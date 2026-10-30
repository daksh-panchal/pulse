[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$dockerCommand = Get-Command docker -ErrorAction SilentlyContinue
$dockerPath = if ($dockerCommand) {
    $dockerCommand.Source
} else {
    Join-Path $env:LOCALAPPDATA 'Programs\DockerDesktop\resources\bin\docker.exe'
}

if (-not (Test-Path -LiteralPath $dockerPath)) {
    throw 'Docker CLI was not found. Install Docker Desktop or add docker.exe to PATH.'
}

function Invoke-Docker {
    param([Parameter(ValueFromRemainingArguments)][string[]]$Arguments)

    & $dockerPath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Docker command failed with exit code $LASTEXITCODE."
    }
}

function Test-PortInUse {
    param([int]$Port)

    $client = [System.Net.Sockets.TcpClient]::new()
    try {
        $task = $client.ConnectAsync('127.0.0.1', $Port)
        return $task.Wait(300) -and $client.Connected
    } catch {
        return $false
    } finally {
        $client.Dispose()
    }
}

Push-Location $repositoryRoot
$applicationStarted = $false
$demoStarted = $false
$serverProcess = $null
$testAgentId = "milestone-one-e2e-$PID"
$serverExecutable = Join-Path $env:TEMP "pulse-e2e-server-$PID.exe"
$agentExecutable = Join-Path $env:TEMP "pulse-e2e-agent-$PID.exe"

try {
    $applicationContainers = & $dockerPath compose -f compose.yaml ps --all --quiet
    $demoContainers = & $dockerPath compose -f deploy/pulse-demo.yaml ps --all --quiet
    if ($applicationContainers -or $demoContainers) {
        throw 'Pulse Compose resources already exist. Stop them before running the isolated end-to-end test.'
    }
    if (Test-PortInUse -Port 8080) {
        throw 'Port 8080 is already in use. The end-to-end test will not stop an unknown process.'
    }

    Invoke-Docker compose -f compose.yaml config --quiet
    Invoke-Docker compose -f deploy/pulse-demo.yaml config --quiet
    Invoke-Docker compose -f compose.yaml up -d --wait postgres
    $applicationStarted = $true

    & go build -o $serverExecutable ./cmd/pulse-server
    if ($LASTEXITCODE -ne 0) { throw 'Could not build the Pulse server.' }
    & go build -o $agentExecutable ./cmd/pulse-agent
    if ($LASTEXITCODE -ne 0) { throw 'Could not build the Pulse agent.' }

    $serverProcess = Start-Process -FilePath $serverExecutable -WorkingDirectory $repositoryRoot -WindowStyle Hidden -PassThru
    $healthy = $false
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        Start-Sleep -Milliseconds 500
        try {
            $health = Invoke-RestMethod http://127.0.0.1:8080/healthz -TimeoutSec 1
            if ($health.status -eq 'ok') {
                $healthy = $true
                break
            }
        } catch {
            # The server may still be starting.
        }
    }
    if (-not $healthy) {
        throw 'Pulse server did not become healthy within 15 seconds.'
    }

    Invoke-Docker compose -f deploy/pulse-demo.yaml up -d --wait
    $demoStarted = $true

    $previousAgentId = $env:PULSE_AGENT_ID
    try {
        $env:PULSE_AGENT_ID = $testAgentId
        & $agentExecutable -once
        if ($LASTEXITCODE -ne 0) { throw 'Pulse agent failed.' }
    } finally {
        if ($null -eq $previousAgentId) {
            Remove-Item Env:PULSE_AGENT_ID -ErrorAction SilentlyContinue
        } else {
            $env:PULSE_AGENT_ID = $previousAgentId
        }
    }

    $overview = Invoke-RestMethod http://127.0.0.1:8080/api/v1/containers
    $container = $overview.containers |
        Where-Object { $_.agent_id -eq $testAgentId -and $_.container_name -eq 'pulse-demo-monitored-container-1' } |
        Select-Object -First 1
    if ($null -eq $container) { throw 'Overview API did not return the test container.' }
    if ($container.state -ne 'running' -or $container.health -ne 'healthy') {
        throw "Unexpected container state: $($container.state)/$($container.health)."
    }
    if ($container.freshness -ne 'current') {
        throw "Unexpected telemetry freshness: $($container.freshness)."
    }
    if ($container.cpu_limit_cores -ne 0.25 -or $container.memory_limit_bytes -ne 67108864) {
        throw 'The API did not return the configured CPU and memory limits.'
    }
    if ($container.labels.PSObject.Properties.Name -contains 'com.docker.compose.project.config_files') {
        throw 'The agent transmitted a Docker Compose host-path label.'
    }

    $history = Invoke-RestMethod "http://127.0.0.1:8080/api/v1/containers/$($container.container_id)/metrics"
    if ($history.samples.Count -lt 1) {
        throw 'Historical API returned no telemetry samples.'
    }

    Write-Host 'Pulse end-to-end verification passed.'
    Write-Host "Container: $($container.container_id)"
    Write-Host "State: $($container.state)/$($container.health), freshness: $($container.freshness)"
} finally {
    if ($serverProcess -and -not $serverProcess.HasExited) {
        Stop-Process -Id $serverProcess.Id -ErrorAction SilentlyContinue
        $serverProcess.WaitForExit(5000) | Out-Null
    }

    if ($applicationStarted) {
        try {
            Invoke-Docker exec pulse-postgres-1 psql -U pulse -d pulse '--set=ON_ERROR_STOP=1' -c "DELETE FROM agents WHERE id = '$testAgentId';"
        } catch {
            Write-Warning "Could not remove the isolated test agent rows: $($_.Exception.Message)"
        }
    }
    if ($demoStarted) {
        try { Invoke-Docker compose -f deploy/pulse-demo.yaml down --remove-orphans } catch { Write-Warning $_ }
    }
    if ($applicationStarted) {
        try { Invoke-Docker compose -f compose.yaml down --remove-orphans } catch { Write-Warning $_ }
    }

    Remove-Item -LiteralPath $serverExecutable -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $agentExecutable -Force -ErrorAction SilentlyContinue
    Pop-Location
}

