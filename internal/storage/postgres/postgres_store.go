package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/daksh-panchal/pulse/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	config.MaxConns = 10
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	store := &Store{pool: pool}
	if err := store.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() { store.pool.Close() }

func (store *Store) Ping(ctx context.Context) error {
	if err := store.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

func (store *Store) Ingest(ctx context.Context, batch telemetry.Batch) (bool, error) {
	receivedAt := time.Now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin ingestion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO agents (id, hostname, first_seen_at, last_seen_at, last_sequence)
		VALUES ($1, $2, $3, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			hostname = EXCLUDED.hostname,
			last_seen_at = GREATEST(agents.last_seen_at, EXCLUDED.last_seen_at),
			last_sequence = GREATEST(agents.last_sequence, EXCLUDED.last_sequence)`,
		batch.Agent.ID, batch.Agent.Hostname, receivedAt, batch.Sequence)
	if err != nil {
		return false, fmt.Errorf("upsert agent: %w", err)
	}

	result, err := tx.Exec(ctx, `
		INSERT INTO telemetry_batches (agent_id, sequence, sent_at, received_at, sample_count)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (agent_id, sequence) DO NOTHING`,
		batch.Agent.ID, batch.Sequence, batch.SentAt, receivedAt, len(batch.Samples))
	if err != nil {
		return false, fmt.Errorf("insert batch: %w", err)
	}
	if result.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit duplicate batch: %w", err)
		}
		return false, nil
	}

	for _, sample := range batch.Samples {
		if err := ingestSample(ctx, tx, batch, sample, receivedAt); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit ingestion: %w", err)
	}
	return true, nil
}

func ingestSample(ctx context.Context, tx pgx.Tx, batch telemetry.Batch, sample telemetry.Sample, receivedAt time.Time) error {
	labels, err := json.Marshal(sample.Labels)
	if err != nil {
		return fmt.Errorf("encode labels for %s: %w", sample.ContainerID, err)
	}
	memoryLimit, err := int64Pointer(sample.MemoryLimitBytes)
	if err != nil {
		return fmt.Errorf("container %s memory limit: %w", sample.ContainerID, err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO container_instances (
			docker_id, agent_id, name, image, platform, created_at, labels,
			first_seen_at, last_seen_at, state, health, cpu_limit_cores,
			memory_limit_bytes, collection_error
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (docker_id) DO UPDATE SET
			agent_id = EXCLUDED.agent_id,
			name = EXCLUDED.name,
			image = EXCLUDED.image,
			platform = EXCLUDED.platform,
			labels = EXCLUDED.labels,
			last_seen_at = EXCLUDED.last_seen_at,
			state = EXCLUDED.state,
			health = EXCLUDED.health,
			cpu_limit_cores = EXCLUDED.cpu_limit_cores,
			memory_limit_bytes = EXCLUDED.memory_limit_bytes,
			collection_error = EXCLUDED.collection_error
		WHERE container_instances.last_seen_at <= EXCLUDED.last_seen_at`,
		sample.ContainerID, batch.Agent.ID, sample.ContainerName, sample.Image,
		sample.Platform, sample.CreatedAt, labels, receivedAt, sample.SampledAt,
		sample.State, sample.Health, sample.CPULimitCores, memoryLimit, sample.CollectionError)
	if err != nil {
		return fmt.Errorf("upsert container %s: %w", sample.ContainerID, err)
	}

	memoryRaw, err := int64Pointer(sample.MemoryRawBytes)
	if err != nil {
		return fmt.Errorf("container %s raw memory: %w", sample.ContainerID, err)
	}
	memoryWorkingSet, err := int64Pointer(sample.MemoryWorkingSetBytes)
	if err != nil {
		return fmt.Errorf("container %s working-set memory: %w", sample.ContainerID, err)
	}
	networkRx, err := int64Pointer(sample.NetworkRxBytes)
	if err != nil {
		return fmt.Errorf("container %s received bytes: %w", sample.ContainerID, err)
	}
	networkTx, err := int64Pointer(sample.NetworkTxBytes)
	if err != nil {
		return fmt.Errorf("container %s transmitted bytes: %w", sample.ContainerID, err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO telemetry_samples (
			agent_id, docker_id, batch_sequence, sampled_at, received_at, state,
			health, cpu_limit_cores, cpu_used_cores, cpu_docker_percent,
			cpu_utilization_percent, memory_raw_bytes, memory_working_set_bytes,
			memory_limit_bytes, memory_utilization_percent, network_rx_bytes,
			network_tx_bytes, collection_error
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (docker_id, sampled_at) DO NOTHING`,
		batch.Agent.ID, sample.ContainerID, batch.Sequence, sample.SampledAt,
		receivedAt, sample.State, sample.Health, sample.CPULimitCores,
		sample.CPUUsedCores, sample.CPUDockerPercent,
		sample.CPUUtilizationPercent, memoryRaw, memoryWorkingSet, memoryLimit,
		sample.MemoryUtilizationPercent, networkRx, networkTx, sample.CollectionError)
	if err != nil {
		return fmt.Errorf("insert sample for %s: %w", sample.ContainerID, err)
	}
	return nil
}

func (store *Store) ListCurrentContainers(ctx context.Context) ([]telemetry.StoredSample, error) {
	rows, err := store.pool.Query(ctx, sampleSelect+`
		WHERE s.id = (
			SELECT newest.id
			FROM telemetry_samples newest
			WHERE newest.docker_id = s.docker_id
			ORDER BY newest.sampled_at DESC, newest.id DESC
			LIMIT 1
		)
		AND s.batch_sequence = (
			SELECT last_sequence FROM agents WHERE id = s.agent_id
		)
		ORDER BY c.name, s.docker_id`)
	if err != nil {
		return nil, fmt.Errorf("query current containers: %w", err)
	}
	defer rows.Close()

	result := make([]telemetry.StoredSample, 0)
	for rows.Next() {
		sample, err := scanStoredSample(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate current containers: %w", err)
	}

	return result, nil
}

func (store *Store) ListMetrics(ctx context.Context, containerID string, from, to time.Time, resolution time.Duration, limit int) ([]telemetry.StoredSample, error) {
	query := sampleSelect + `
		WHERE s.id IN (
			SELECT id FROM telemetry_samples
			WHERE docker_id = $1 AND sampled_at >= $2 AND sampled_at <= $3
			ORDER BY sampled_at DESC, id DESC
			LIMIT $4
		)
		ORDER BY s.sampled_at ASC, s.id ASC`
	arguments := []any{containerID, from, to, limit}
	if resolution > 0 {
		query = sampleSelect + `
			WHERE s.id IN (
				SELECT DISTINCT ON (floor(extract(epoch FROM sampled_at) / $5)) id
				FROM telemetry_samples
				WHERE docker_id = $1 AND sampled_at >= $2 AND sampled_at <= $3
				ORDER BY floor(extract(epoch FROM sampled_at) / $5) DESC,
					sampled_at DESC, id DESC
				LIMIT $4
			)
			ORDER BY s.sampled_at ASC, s.id ASC`
		arguments = append(arguments, resolution.Seconds())
	}
	rows, err := store.pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	defer rows.Close()

	result := make([]telemetry.StoredSample, 0)
	for rows.Next() {
		sample, err := scanStoredSample(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metrics: %w", err)
	}
	return result, nil
}

const sampleSelect = `
	SELECT s.agent_id, s.docker_id, c.name, c.image, c.platform, c.created_at,
		c.labels, s.sampled_at, s.received_at, s.state, s.health,
		s.cpu_limit_cores, s.cpu_used_cores, s.cpu_docker_percent,
		s.cpu_utilization_percent, s.memory_raw_bytes,
		s.memory_working_set_bytes, s.memory_limit_bytes,
		s.memory_utilization_percent, s.network_rx_bytes, s.network_tx_bytes,
		s.collection_error
	FROM telemetry_samples s
	JOIN container_instances c ON c.docker_id = s.docker_id`

type rowScanner interface {
	Scan(...any) error
}

func scanStoredSample(row rowScanner) (telemetry.StoredSample, error) {
	var sample telemetry.StoredSample
	var labels []byte
	var memoryRaw, memoryWorkingSet, memoryLimit, networkRx, networkTx *int64
	err := row.Scan(
		&sample.AgentID, &sample.ContainerID, &sample.ContainerName, &sample.Image,
		&sample.Platform, &sample.CreatedAt, &labels, &sample.SampledAt,
		&sample.ReceivedAt, &sample.State, &sample.Health, &sample.CPULimitCores,
		&sample.CPUUsedCores, &sample.CPUDockerPercent,
		&sample.CPUUtilizationPercent, &memoryRaw, &memoryWorkingSet, &memoryLimit,
		&sample.MemoryUtilizationPercent, &networkRx, &networkTx,
		&sample.CollectionError,
	)
	if err != nil {
		return telemetry.StoredSample{}, fmt.Errorf("scan telemetry sample: %w", err)
	}
	if len(labels) > 0 {
		if err := json.Unmarshal(labels, &sample.Labels); err != nil {
			return telemetry.StoredSample{}, fmt.Errorf("decode stored labels: %w", err)
		}
	}
	sample.MemoryRawBytes = uint64Pointer(memoryRaw)
	sample.MemoryWorkingSetBytes = uint64Pointer(memoryWorkingSet)
	sample.MemoryLimitBytes = uint64Pointer(memoryLimit)
	sample.NetworkRxBytes = uint64Pointer(networkRx)
	sample.NetworkTxBytes = uint64Pointer(networkTx)
	return sample, nil
}

func int64Pointer(value *uint64) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	if *value > math.MaxInt64 {
		return nil, fmt.Errorf("value %d exceeds PostgreSQL BIGINT", *value)
	}
	converted := int64(*value)
	return &converted, nil
}

func uint64Pointer(value *int64) *uint64 {
	if value == nil || *value < 0 {
		return nil
	}
	converted := uint64(*value)
	return &converted
}
