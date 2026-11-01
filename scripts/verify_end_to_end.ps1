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
        throw "Docker command '$($Arguments -join ' ')' failed with exit code $LASTEXITCODE."
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

function Start-PulseServer {
    param(
        [string]$Executable,
        [string]$WorkingDirectory
    )

    $process = Start-Process -FilePath $Executable -WorkingDirectory $WorkingDirectory -WindowStyle Hidden -PassThru
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        Start-Sleep -Milliseconds 500
        try {
            $health = Invoke-RestMethod http://127.0.0.1:8080/healthz -TimeoutSec 1
            if ($health.status -eq 'ok') {
                return $process
            }
        } catch {
            # The server may still be starting.
        }
    }
    if (-not $process.HasExited) {
        Stop-Process -Id $process.Id -ErrorAction SilentlyContinue
    }
    throw 'Pulse server did not become healthy within 15 seconds.'
}

function Wait-PulseIncident {
    param(
        [string]$Kind,
        [string]$State,
        [string]$AgentId,
        [string]$View = 'all',
        [int]$TimeoutSeconds = 20
    )

    $deadline = [DateTimeOffset]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        $response = Invoke-RestMethod "http://127.0.0.1:8080/api/v1/incidents?view=$View&limit=1000"
        $incident = $response.incidents |
            Where-Object { $_.kind -eq $Kind -and $_.state -eq $State -and $_.agent_id -eq $AgentId } |
            Select-Object -First 1
        if ($null -ne $incident) {
            return $incident
        }
        Start-Sleep -Milliseconds 500
    } while ([DateTimeOffset]::UtcNow -lt $deadline)
    throw "Incident $Kind did not reach $State within $TimeoutSeconds seconds."
}

Push-Location $repositoryRoot
$applicationStarted = $false
$demoStarted = $false
$serverProcess = $null
$agentProcess = $null
$testAgentId = "milestone-two-e2e-$PID"
$serverExecutable = Join-Path $env:TEMP "pulse-e2e-server-$PID.exe"
$agentExecutable = Join-Path $env:TEMP "pulse-e2e-agent-$PID.exe"
$spoolDirectory = Join-Path $env:TEMP "pulse-e2e-spool-$PID"
$environmentNames = @(
    'PULSE_AGENT_ID', 'PULSE_AGENT_INTERVAL', 'PULSE_AGENT_SPOOL_DIR',
    'PULSE_ALERT_RESOURCE_SUSTAIN', 'PULSE_ALERT_AVAILABILITY_SUSTAIN',
    'PULSE_ALERT_RECOVERY', 'PULSE_ALERT_AGENT_STALE_AFTER',
    'PULSE_ALERT_CONTAINER_STALE_AFTER', 'PULSE_ALERT_EVALUATION_INTERVAL'
)
$previousEnvironment = @{}
$environmentNames | ForEach-Object { $previousEnvironment[$_] = [Environment]::GetEnvironmentVariable($_) }

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

    $env:PULSE_ALERT_RESOURCE_SUSTAIN = '3s'
    $env:PULSE_ALERT_AVAILABILITY_SUSTAIN = '2s'
    $env:PULSE_ALERT_RECOVERY = '2s'
    $env:PULSE_ALERT_AGENT_STALE_AFTER = '8s'
    $env:PULSE_ALERT_CONTAINER_STALE_AFTER = '4s'
    $env:PULSE_ALERT_EVALUATION_INTERVAL = '500ms'
    $serverProcess = Start-PulseServer -Executable $serverExecutable -WorkingDirectory $repositoryRoot

    Invoke-Docker compose -f deploy/pulse-demo.yaml up -d --wait
    $demoStarted = $true

    $env:PULSE_AGENT_ID = $testAgentId
    $env:PULSE_AGENT_INTERVAL = '1s'
    $env:PULSE_AGENT_SPOOL_DIR = $spoolDirectory
    $agentProcess = Start-Process -FilePath $agentExecutable -WorkingDirectory $repositoryRoot -WindowStyle Hidden -PassThru

    $container = $null
    for ($attempt = 0; $attempt -lt 20 -and $null -eq $container; $attempt++) {
        Start-Sleep -Milliseconds 500
        $overview = Invoke-RestMethod http://127.0.0.1:8080/api/v1/containers
        $container = $overview.containers |
            Where-Object { $_.agent_id -eq $testAgentId -and $_.container_name -eq 'pulse-demo-monitored-container-1' } |
            Select-Object -First 1
    }
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

    Invoke-Docker -Arguments @('exec', '-d', 'pulse-demo-monitored-container-1', 'timeout', '10', 'sh', '-c', 'while :; do :; done')
    $highCpuIncident = Wait-PulseIncident -Kind 'high_cpu' -State 'firing' -AgentId $testAgentId -View 'active' -TimeoutSeconds 15
    $null = Wait-PulseIncident -Kind 'high_cpu' -State 'resolved' -AgentId $testAgentId -View 'history' -TimeoutSeconds 20

    Invoke-Docker compose -f deploy/pulse-demo.yaml stop monitored-container
    $unavailableIncident = Wait-PulseIncident -Kind 'container_unavailable' -State 'firing' -AgentId $testAgentId -View 'active' -TimeoutSeconds 10
    Invoke-Docker compose -f deploy/pulse-demo.yaml start monitored-container
    $null = Wait-PulseIncident -Kind 'container_unavailable' -State 'resolved' -AgentId $testAgentId -View 'history' -TimeoutSeconds 15

    Stop-Process -Id $agentProcess.Id
    $agentProcess.WaitForExit(5000) | Out-Null
    $agentProcess = $null
    $null = Wait-PulseIncident -Kind 'agent_stale' -State 'firing' -AgentId $testAgentId -View 'active' -TimeoutSeconds 15
    $agentProcess = Start-Process -FilePath $agentExecutable -WorkingDirectory $repositoryRoot -WindowStyle Hidden -PassThru
    $null = Wait-PulseIncident -Kind 'agent_stale' -State 'resolved' -AgentId $testAgentId -View 'history' -TimeoutSeconds 15

    Stop-Process -Id $serverProcess.Id
    $serverProcess.WaitForExit(5000) | Out-Null
    $serverProcess = $null
    Start-Sleep -Seconds 3
    $queuedBeforeRecovery = @(Get-ChildItem -LiteralPath $spoolDirectory -Filter '*.json' -File).Count
    if ($queuedBeforeRecovery -lt 1) {
        throw 'The agent did not spool telemetry while the server was unavailable.'
    }
    $serverProcess = Start-PulseServer -Executable $serverExecutable -WorkingDirectory $repositoryRoot
    $deadline = [DateTimeOffset]::UtcNow.AddSeconds(15)
    do {
        $queuedAfterRecovery = @(Get-ChildItem -LiteralPath $spoolDirectory -Filter '*.json' -File).Count
        if ($queuedAfterRecovery -eq 0) { break }
        Start-Sleep -Milliseconds 500
    } while ([DateTimeOffset]::UtcNow -lt $deadline)
    if ($queuedAfterRecovery -ne 0) {
        throw 'The agent did not replay queued telemetry after the server recovered.'
    }

    Write-Host 'Pulse Milestone 2 end-to-end verification passed.'
    Write-Host "Container: $($container.container_id)"
    Write-Host "State: $($container.state)/$($container.health), freshness: $($container.freshness)"
    Write-Host "High CPU incident: $($highCpuIncident.id), unavailable incident: $($unavailableIncident.id)"
    Write-Host "Replayed queued batches after interruption: $queuedBeforeRecovery"
} finally {
    if ($agentProcess -and -not $agentProcess.HasExited) {
        Stop-Process -Id $agentProcess.Id -ErrorAction SilentlyContinue
        $agentProcess.WaitForExit(5000) | Out-Null
    }
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
    Remove-Item -LiteralPath $spoolDirectory -Recurse -Force -ErrorAction SilentlyContinue
    foreach ($name in $environmentNames) {
        [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name])
    }
    Pop-Location
}

