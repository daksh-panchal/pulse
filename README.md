# Pulse

Pulse is a standalone infrastructure monitoring platform for Docker workloads.
It uses a Go agent to collect real CPU, memory, network, state, and health data
from Docker Desktop, sends that telemetry to a Go backend, and stores current
and historical measurements in PostgreSQL.

The monitoring backend, agent, retry handling, and incident detection are
functional. The web dashboard is still under development. Pulse never generates
fake measurements for its APIs or future dashboard.

## Architecture

```text
Docker Desktop
      |
      | Docker Engine API
      v
Pulse agent ---- telemetry batches ----> Pulse server ----> PostgreSQL
     |                                    |       |
     | bounded retry spool                |       v
     +------------------------------------+  alert evaluation
                                                  |
                                                  v
                                      telemetry and incident APIs
```

- The **agent** discovers only containers labelled `pulse.monitor=true`, reads
  Docker statistics, calculates resource utilization, and sends versioned
  batches at a configurable interval.
- The **server** validates batches, prevents duplicate ingestion, stores them in
  one transaction, evaluates alert conditions, and exposes telemetry and
  incident history.
- **PostgreSQL** stores agents, batch identities, container instances, and
  time-series samples. Docker IDs keep recreated containers separate.

The server is a modular monolith rather than a collection of microservices.
Only the agent can access Docker; the backend and future browser dashboard
cannot control containers.

## Telemetry semantics

Docker reports cumulative counters, so Pulse converts them into measurements
that are useful for monitoring:

- **CPU used cores** compares the change in container CPU time with the change
  in system CPU time, then accounts for the CPUs available to Docker.
- **Docker CPU percent** treats one fully used core as 100%, so it can exceed
  100% on multicore systems.
- **CPU utilization** compares used cores with the container's configured CPU
  limit. A container using `0.20` cores with a `0.25`-core limit is at 80%.
- **Raw memory** is Docker's complete usage value. **Working-set memory** removes
  reclaimable inactive file cache and is used for memory utilization.
- **Network RX/TX** are cumulative byte totals across all container interfaces.
  Transfer rates can be calculated from the difference between timed samples.
- **State** and Docker **health** are stored separately. Stopped containers are
  retained in the inventory but do not receive runtime metric values.

Unavailable metrics are stored as `null`, not zero. Current-container responses
also classify telemetry as `current`, `delayed`, or `stale`, preventing old data
from being presented as live data.

## Reliability and incidents

Failed deliveries are written atomically to a bounded local spool and retried
oldest-first, including after an agent restart. By default, up to 256 batches are
retained so a prolonged outage cannot consume unbounded disk space.

Pulse detects sustained high CPU or memory, unavailable containers, and agents
that stop reporting. Each condition has one active incident that progresses
through `pending`, `firing`, `recovering`, and `resolved`; repeated bad samples
update that incident instead of creating duplicates. Unknown or stale telemetry
never resolves an incident because missing data is not proof of recovery.

## Key design decisions

- All displayed data must originate from Docker and the Pulse backend.
- Containers opt in through a label; Pulse does not scan unrelated workloads.
- CPU and memory percentages are relative to container limits, not the laptop's
  total capacity.
- Batches have agent-specific sequence numbers for idempotent ingestion.
- Batch storage is transactional, so partial batches are not committed.
- Query ranges, request sizes, sample counts, and container resources are
  bounded.
- Images used by the local environment are pinned to verified digests.

## Technology stack

- Go 1.27
- Docker Engine API through the Moby client
- PostgreSQL 17 through `pgx`
- Versioned JSON REST APIs
- Docker Desktop and Docker Compose
- Go unit, HTTP contract, PostgreSQL integration, and end-to-end tests

## Local setup

Development currently targets Windows with Docker Desktop running Linux
containers. Go 1.27, Docker Compose, Git, and PowerShell are required.

Start PostgreSQL:

```powershell
docker compose up -d --wait postgres
```

Start the server:

```powershell
go run ./cmd/pulse-server
```

In another terminal, start the constrained demo container and agent:

```powershell
docker compose -f deploy/pulse-demo.yaml up -d --wait
go run ./cmd/pulse-agent
```

The demo container is limited to `0.25` CPU, `64 MiB` memory, and 32 processes.
It runs as a non-root user with a read-only filesystem, no capabilities, no host
mounts, and no published ports.

### API

```text
GET  /healthz
POST /api/v1/telemetry/batches
GET  /api/v1/containers
GET  /api/v1/containers/{full-docker-id}/metrics?from=&to=&limit=
GET  /api/v1/incidents?view=active|history|all&limit=
```

Query the latest observations and then one container's recent history:

```powershell
$overview = Invoke-RestMethod http://127.0.0.1:8080/api/v1/containers
$containerId = $overview.containers[0].container_id
Invoke-RestMethod "http://127.0.0.1:8080/api/v1/containers/$containerId/metrics"
```

Configuration can be overridden with `PULSE_DATABASE_URL`,
`PULSE_SERVER_ADDRESS`, `PULSE_SERVER_URL`, `PULSE_AGENT_ID`,
`PULSE_AGENT_INTERVAL`, `PULSE_AGENT_REQUEST_TIMEOUT`, `PULSE_DOCKER_LABEL`, and
`PULSE_POSTGRES_PORT`. Retry storage uses `PULSE_AGENT_SPOOL_DIR` and
`PULSE_AGENT_SPOOL_MAX_BATCHES`. Alert thresholds and timing use the
`PULSE_ALERT_*` variables defined in `cmd/pulse-server/pulse_server.go`.

## Testing

Run unit and HTTP contract tests plus static checks:

```powershell
go test ./...
go vet ./...
```

Run the opt-in PostgreSQL integration test against the Pulse database:

```powershell
$env:PULSE_TEST_DATABASE_URL = "postgres://pulse:pulse_dev@127.0.0.1:5433/pulse?sslmode=disable"
go test ./internal/storage/postgres -run "TestStoreIngestAndQuery|TestAlertSignalsAndIncidentPersistence" -v
Remove-Item Env:PULSE_TEST_DATABASE_URL
```

Run the real Docker-to-agent-to-server-to-PostgreSQL verification:

```powershell
.\scripts\verify_end_to_end.ps1
```

The end-to-end test uses real, resource-limited CPU pressure and verifies
container recovery, stale-agent detection, communication interruption, and
queued telemetry replay. Tests use unique data and clean up only Pulse-owned
resources.

## Safety and cleanup

Pulse uses modest resource limits, local-only service ports, digest-pinned
images, and explicitly labelled resources. Demo containers have no privileged
access, Docker socket mount, personal-directory mount, or unbounded workload.

Stop the demo and database without deleting stored telemetry:

```powershell
docker compose -f deploy/pulse-demo.yaml down --remove-orphans
docker compose down
```

The PostgreSQL volume is preserved. `docker compose down --volumes` should only
be used when intentionally deleting all Pulse development data after confirming
that the Compose project name is `pulse`.
