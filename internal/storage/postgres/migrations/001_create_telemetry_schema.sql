CREATE TABLE agents (
    id TEXT PRIMARY KEY,
    hostname TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    last_sequence BIGINT NOT NULL CHECK (last_sequence > 0)
);

CREATE TABLE telemetry_batches (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    sent_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    sample_count INTEGER NOT NULL CHECK (sample_count >= 0),
    PRIMARY KEY (agent_id, sequence)
);

CREATE TABLE container_instances (
    docker_id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    image TEXT NOT NULL,
    platform TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    labels JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    state TEXT NOT NULL,
    health TEXT NOT NULL,
    cpu_limit_cores DOUBLE PRECISION,
    memory_limit_bytes BIGINT,
    collection_error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX container_instances_agent_last_seen_idx
    ON container_instances (agent_id, last_seen_at DESC);

CREATE TABLE telemetry_samples (
    id BIGSERIAL PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    docker_id TEXT NOT NULL REFERENCES container_instances(docker_id) ON DELETE CASCADE,
    batch_sequence BIGINT NOT NULL,
    sampled_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    state TEXT NOT NULL,
    health TEXT NOT NULL,
    cpu_limit_cores DOUBLE PRECISION,
    cpu_used_cores DOUBLE PRECISION,
    cpu_docker_percent DOUBLE PRECISION,
    cpu_utilization_percent DOUBLE PRECISION,
    memory_raw_bytes BIGINT,
    memory_working_set_bytes BIGINT,
    memory_limit_bytes BIGINT,
    memory_utilization_percent DOUBLE PRECISION,
    network_rx_bytes BIGINT,
    network_tx_bytes BIGINT,
    collection_error TEXT NOT NULL DEFAULT '',
    CONSTRAINT telemetry_samples_batch_fk
        FOREIGN KEY (agent_id, batch_sequence)
        REFERENCES telemetry_batches(agent_id, sequence)
        ON DELETE CASCADE,
    CONSTRAINT telemetry_samples_instance_time_unique
        UNIQUE (docker_id, sampled_at)
);

CREATE INDEX telemetry_samples_instance_time_idx
    ON telemetry_samples (docker_id, sampled_at DESC);

CREATE INDEX telemetry_samples_received_at_idx
    ON telemetry_samples (received_at DESC);

