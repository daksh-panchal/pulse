# Pulse

Pulse is a planned infrastructure monitoring and observability platform for
real Docker workloads. It will use a Go agent and backend, PostgreSQL, and a web
dashboard to collect and investigate container metrics and incidents.

The project is currently in its architecture and telemetry-validation phase;
there is no production monitoring service yet.

## Current artifacts

- [Architecture and milestone plan](docs/architecture.md)
- [Initial telemetry spike findings](docs/telemetry-spike-findings.md)
- [Read-only Docker telemetry spike guide](docs/docker-telemetry-spike-guide.md)
- [Safe telemetry fixture guide](docs/docker-telemetry-fixture-guide.md)

The spike discovers only containers explicitly labeled `pulse.monitor=true`
and does not create, start, stop, update, or remove Docker resources.
