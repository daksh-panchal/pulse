# Docker telemetry spike findings

Date: 2026-10-26 (Pacific/Auckland)

## Scope of the initial run

The initial run was intentionally limited to read-only Docker Engine operations:
ping, label-filtered container listing, container inspection, and one-shot stats.
No containers, images, networks, volumes, or Docker settings were created,
modified, started, stopped, or removed.

## Environment

- Windows with Docker Desktop's default Engine named pipe
  (`npipe:////./pipe/docker_engine`)
- Go 1.27.1 (`windows/amd64`)
- Moby Engine client module `github.com/moby/moby/client` v0.6.2
- Moby API types resolved transitively at v1.56.1

The Docker CLI executable was not available on the current `PATH`, but the
Docker Engine itself was reachable through the named pipe by the Go client.

## Initial results

- The client successfully negotiated with the Docker Engine.
- Label-scoped discovery using `pulse.monitor=true` completed successfully.
- Zero matching containers were present.
- The program returned a valid JSON envelope with collection time, applied
  filter, and an empty container list.
- Unit tests cover CPU deltas, CPU-limit precedence, cpuset parsing, memory
  working set, and network counter aggregation.
- `go test ./...` and `go vet ./...` pass.
- `go test -race ./...` could not run because CGO is disabled in the current Go
  environment. The spike is single-threaded, but race testing should be enabled
  when the concurrent production agent exists.

## Constrained fixture validation

A separately defined Compose fixture was then reviewed and run. It used one
`alpine:3.20.3` container with:

- `pulse.monitor=true` and Pulse ownership labels;
- a 0.25 CPU limit;
- a 64 MiB memory limit;
- a 32-process limit;
- a read-only root filesystem and 8 MiB `/tmp`;
- all Linux capabilities dropped and `no-new-privileges` enabled;
- no privileged mode, host-directory mounts, or published ports; and
- a local health check and no automatic restart.

Docker inspection confirmed that these restrictions were enforced, including
`NanoCpus=250000000`, `Memory=67108864`, `PidsLimit=32`,
`ReadonlyRootfs=true`, `Privileged=false`, `CapDrop=["ALL"]`, and no port
bindings.

### Idle observation

Pulse discovered the fixture without being given its ID and reported:

- the exact Docker container ID and creation time;
- platform `linux`, state `running`, and health `healthy`;
- CPU limit `0.25` cores;
- memory limit `67,108,864` bytes;
- network receive/transmit totals `1,752 / 126` bytes; and
- low idle CPU and memory utilization.

The network totals matched `docker stats` (`1.75 kB / 126 B`). Memory values
were close but not identical because Pulse and Docker CLI samples were taken
sequentially while the periodic health-check process was starting and exiting.
Future automated comparisons should compare raw Engine samples or allow an
explicit tolerance rather than expect two non-simultaneous readings to match
byte-for-byte.

### Bounded CPU observation

An eight-second `yes` process was run inside the resource-limited container and
automatically terminated by BusyBox `timeout`. During that interval:

- Docker CLI reported `24.75%` CPU, where 100% represents one host core;
- Pulse measured `0.2497` cores used and Docker-style CPU `24.97%`; and
- Pulse normalized this to `99.88%` of the container's 0.25-core limit.

This validates both the Docker counter calculation and the distinction between
host-core percentage and utilization relative to a container limit. After nine
seconds, `docker top` confirmed that the load process was gone and only the
fixture's normal init, shell, and sleep processes remained.

### Stop, recovery, and cleanup

After Compose stopped the fixture, Pulse reported the same container instance
as `exited` and did not request unavailable runtime stats. After Compose started
it again and its health check passed, Pulse reported `running/healthy` and
resumed metric collection.

Finally, `docker compose down --remove-orphans` removed the fixture container
and its dedicated network. Both Compose project listing and a Docker label
query confirmed that no Pulse-owned fixture containers remained. The downloaded
Alpine image was intentionally left in Docker's shared image cache.

## Remaining validation

The spike has not yet tested:

- container recreation producing a new immutable Docker ID;
- network transfer rates across adjacent samples;
- memory pressure near the configured limit;
- Windows containers; or
- monitoring-path interruption, batching, retry, and stale-data behavior.

Those belong in later focused tests. The experiment could not create or mutate
Docker resources; Compose exclusively owned fixture lifecycle. The standalone
experiment was retired after Milestone 1 promoted its validated metric logic
into the production agent.

