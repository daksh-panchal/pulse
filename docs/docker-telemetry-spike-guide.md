# Docker telemetry spike guide

This is a disposable, read-only experiment for validating Docker Desktop metric
semantics before Pulse's production agent is designed. It performs only Docker
ping, container list, container inspect, and one-shot container stats calls.
It never creates, starts, stops, updates, or removes Docker resources.

By default it discovers only containers with `pulse.monitor=true` and includes
stopped containers in the inventory.

```powershell
cd spikes/docker-telemetry
go run .
```

Use a different opt-in label only when deliberately testing discovery:

```powershell
go run . -label 'example.monitor=true'
```

Docker Desktop must be running and its Engine endpoint must be accessible to the
current Windows user. `DOCKER_HOST`, `DOCKER_TLS_VERIFY`, and `DOCKER_CERT_PATH`
are honored by the Moby client. Output is JSON so it can be saved and compared
with these read-only commands:

```powershell
docker ps --all --filter 'label=pulse.monitor=true'
docker stats --no-stream <container>
docker inspect <container>
```

The CPU calculation targets Linux containers. Unsupported or insufficient data
is reported by omitting the calculated percentage and adding a note; the spike
does not turn unknown values into zero.

For an intentionally constrained container that can be used to validate real
measurements, see the [telemetry fixture guide](docker-telemetry-fixture-guide.md).
Its Compose lifecycle is kept separate so this read-only spike never gains
Docker mutation capabilities.

