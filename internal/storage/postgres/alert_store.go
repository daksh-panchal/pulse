package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/daksh-panchal/pulse/internal/alerting"
	"github.com/jackc/pgx/v5"
)

func (store *Store) ListAlertSignals(ctx context.Context, now time.Time, policy alerting.Policy) ([]alerting.Signal, error) {
	signals, err := store.listAgentSignals(ctx, now, policy)
	if err != nil {
		return nil, err
	}
	containerSignals, err := store.listContainerSignals(ctx, now, policy)
	if err != nil {
		return nil, err
	}
	return append(signals, containerSignals...), nil
}

func (store *Store) listAgentSignals(ctx context.Context, now time.Time, policy alerting.Policy) ([]alerting.Signal, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, last_seen_at FROM agents ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query agent alert signals: %w", err)
	}
	defer rows.Close()

	signals := make([]alerting.Signal, 0)
	for rows.Next() {
		var agentID string
		var lastSeenAt time.Time
		if err := rows.Scan(&agentID, &lastSeenAt); err != nil {
			return nil, fmt.Errorf("scan agent alert signal: %w", err)
		}
		signals = append(signals, alerting.Signal{
			Fingerprint: "agent_stale:" + agentID,
			Kind:        alerting.KindAgentStale,
			AgentID:     agentID,
			Title:       "Monitoring agent stopped reporting",
			Known:       true,
			Breached:    now.Sub(lastSeenAt) > policy.AgentStaleAfter,
			ObservedAt:  lastSeenAt,
			Details:     map[string]any{"last_seen_at": lastSeenAt},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent alert signals: %w", err)
	}
	return signals, nil
}

func (store *Store) listContainerSignals(ctx context.Context, now time.Time, policy alerting.Policy) ([]alerting.Signal, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT c.docker_id, c.agent_id, c.name, a.last_seen_at,
			s.sampled_at, s.state, s.health,
			s.cpu_utilization_percent, s.memory_utilization_percent
		FROM container_instances c
		JOIN agents a ON a.id = c.agent_id
		JOIN LATERAL (
			SELECT sampled_at, state, health, cpu_utilization_percent,
				memory_utilization_percent
			FROM telemetry_samples
			WHERE docker_id = c.docker_id
			ORDER BY sampled_at DESC, id DESC
			LIMIT 1
		) s ON true
		ORDER BY c.name, c.docker_id`)
	if err != nil {
		return nil, fmt.Errorf("query container alert signals: %w", err)
	}
	defer rows.Close()

	signals := make([]alerting.Signal, 0)
	for rows.Next() {
		var containerID, agentID, name, state, health string
		var agentLastSeenAt, sampledAt time.Time
		var cpu, memory *float64
		if err := rows.Scan(&containerID, &agentID, &name, &agentLastSeenAt, &sampledAt, &state, &health, &cpu, &memory); err != nil {
			return nil, fmt.Errorf("scan container alert signal: %w", err)
		}
		agentFresh := now.Sub(agentLastSeenAt) <= policy.AgentStaleAfter
		sampleFresh := now.Sub(sampledAt) <= policy.ContainerStaleAfter
		details := map[string]any{
			"container_name": name,
			"state":          state,
			"health":         health,
			"sampled_at":     sampledAt,
		}
		availabilityBreached := !strings.EqualFold(state, "running") ||
			strings.EqualFold(health, "unhealthy") || !sampleFresh
		signals = append(signals, alerting.Signal{
			Fingerprint: "container_unavailable:" + containerID,
			Kind:        alerting.KindContainerUnavailable,
			AgentID:     agentID,
			ContainerID: containerID,
			Title:       fmt.Sprintf("Container %s is unavailable", name),
			Known:       agentFresh,
			Breached:    agentFresh && availabilityBreached,
			ObservedAt:  sampledAt,
			Details:     details,
		})

		cpuThreshold := policy.CPUThresholdPercent
		signals = append(signals, alerting.Signal{
			Fingerprint:   "high_cpu:" + containerID,
			Kind:          alerting.KindHighCPU,
			AgentID:       agentID,
			ContainerID:   containerID,
			Title:         fmt.Sprintf("Container %s has high CPU utilization", name),
			Known:         agentFresh && sampleFresh && strings.EqualFold(state, "running") && cpu != nil,
			Breached:      cpu != nil && *cpu >= cpuThreshold,
			Threshold:     &cpuThreshold,
			ObservedValue: cpu,
			ObservedAt:    sampledAt,
			Details:       details,
		})

		memoryThreshold := policy.MemoryThresholdPercent
		signals = append(signals, alerting.Signal{
			Fingerprint:   "high_memory:" + containerID,
			Kind:          alerting.KindHighMemory,
			AgentID:       agentID,
			ContainerID:   containerID,
			Title:         fmt.Sprintf("Container %s has high memory utilization", name),
			Known:         agentFresh && sampleFresh && strings.EqualFold(state, "running") && memory != nil,
			Breached:      memory != nil && *memory >= memoryThreshold,
			Threshold:     &memoryThreshold,
			ObservedValue: memory,
			ObservedAt:    sampledAt,
			Details:       details,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate container alert signals: %w", err)
	}
	return signals, nil
}

func (store *Store) FindActiveIncident(ctx context.Context, fingerprint string) (*alerting.Incident, error) {
	row := store.pool.QueryRow(ctx, incidentSelect+`
		WHERE fingerprint = $1 AND resolved_at IS NULL
		ORDER BY opened_at DESC
		LIMIT 1`, fingerprint)
	incident, err := scanIncident(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &incident, nil
}

func (store *Store) CreateIncident(ctx context.Context, incident alerting.Incident) (alerting.Incident, error) {
	details, err := json.Marshal(incident.Details)
	if err != nil {
		return alerting.Incident{}, fmt.Errorf("encode incident details: %w", err)
	}
	row := store.pool.QueryRow(ctx, `
		INSERT INTO incidents (
			fingerprint, kind, agent_id, docker_id, state, title, threshold,
			observed_value, opened_at, firing_at, recovery_started_at, resolved_at,
			last_evaluated_at, last_observed_at, details
		) VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id`, incident.Fingerprint, incident.Kind, incident.AgentID,
		incident.ContainerID, incident.State, incident.Title, incident.Threshold,
		incident.ObservedValue, incident.OpenedAt, incident.FiringAt,
		incident.RecoveryStartedAt, incident.ResolvedAt, incident.LastEvaluatedAt,
		incident.LastObservedAt, details)
	if err := row.Scan(&incident.ID); err != nil {
		return alerting.Incident{}, fmt.Errorf("insert incident: %w", err)
	}
	return incident, nil
}

func (store *Store) UpdateIncident(ctx context.Context, incident alerting.Incident) error {
	details, err := json.Marshal(incident.Details)
	if err != nil {
		return fmt.Errorf("encode incident details: %w", err)
	}
	_, err = store.pool.Exec(ctx, `
		UPDATE incidents SET state=$2, observed_value=$3, firing_at=$4,
			recovery_started_at=$5, resolved_at=$6, last_evaluated_at=$7,
			last_observed_at=$8, details=$9
		WHERE id=$1`, incident.ID, incident.State, incident.ObservedValue,
		incident.FiringAt, incident.RecoveryStartedAt, incident.ResolvedAt,
		incident.LastEvaluatedAt, incident.LastObservedAt, details)
	if err != nil {
		return fmt.Errorf("update incident: %w", err)
	}
	return nil
}

func (store *Store) ListIncidents(ctx context.Context, view alerting.View, limit int) ([]alerting.Incident, error) {
	where := ""
	switch view {
	case alerting.ViewActive:
		where = "WHERE resolved_at IS NULL"
	case alerting.ViewHistory:
		where = "WHERE resolved_at IS NOT NULL"
	case alerting.ViewAll:
	default:
		return nil, fmt.Errorf("unsupported incident view %q", view)
	}
	rows, err := store.pool.Query(ctx, incidentSelect+" "+where+`
		ORDER BY opened_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query incidents: %w", err)
	}
	defer rows.Close()
	incidents := make([]alerting.Incident, 0)
	for rows.Next() {
		incident, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		incidents = append(incidents, incident)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incidents: %w", err)
	}
	return incidents, nil
}

const incidentSelect = `
	SELECT id, fingerprint, kind, agent_id, COALESCE(docker_id, ''), state,
		title, threshold, observed_value, opened_at, firing_at,
		recovery_started_at, resolved_at, last_evaluated_at, last_observed_at,
		details
	FROM incidents`

func scanIncident(row rowScanner) (alerting.Incident, error) {
	var incident alerting.Incident
	var details []byte
	if err := row.Scan(&incident.ID, &incident.Fingerprint, &incident.Kind,
		&incident.AgentID, &incident.ContainerID, &incident.State, &incident.Title,
		&incident.Threshold, &incident.ObservedValue, &incident.OpenedAt,
		&incident.FiringAt, &incident.RecoveryStartedAt, &incident.ResolvedAt,
		&incident.LastEvaluatedAt, &incident.LastObservedAt, &details); err != nil {
		return alerting.Incident{}, err
	}
	if len(details) > 0 {
		if err := json.Unmarshal(details, &incident.Details); err != nil {
			return alerting.Incident{}, fmt.Errorf("decode incident details: %w", err)
		}
	}
	return incident, nil
}
