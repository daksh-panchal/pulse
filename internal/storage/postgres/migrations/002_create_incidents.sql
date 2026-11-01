CREATE TABLE incidents (
    id BIGSERIAL PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN (
        'high_cpu', 'high_memory', 'container_unavailable', 'agent_stale'
    )),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    docker_id TEXT REFERENCES container_instances(docker_id) ON DELETE CASCADE,
    state TEXT NOT NULL CHECK (state IN ('pending', 'firing', 'recovering', 'resolved')),
    title TEXT NOT NULL,
    threshold DOUBLE PRECISION,
    observed_value DOUBLE PRECISION,
    opened_at TIMESTAMPTZ NOT NULL,
    firing_at TIMESTAMPTZ,
    recovery_started_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    last_evaluated_at TIMESTAMPTZ NOT NULL,
    last_observed_at TIMESTAMPTZ NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE UNIQUE INDEX incidents_active_fingerprint_unique
    ON incidents (fingerprint)
    WHERE resolved_at IS NULL;

CREATE INDEX incidents_state_opened_idx
    ON incidents (state, opened_at DESC);

CREATE INDEX incidents_container_opened_idx
    ON incidents (docker_id, opened_at DESC)
    WHERE docker_id IS NOT NULL;
