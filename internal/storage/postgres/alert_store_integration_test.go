package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/daksh-panchal/pulse/internal/alerting"
	"github.com/daksh-panchal/pulse/internal/telemetry"
)

func TestAlertSignalsAndIncidentPersistence(t *testing.T) {
	databaseURL := os.Getenv("PULSE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PULSE_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	agentID := fmt.Sprintf("alert-integration-%d", now.UnixNano())
	containerID := strings.Repeat("d", 48) + fmt.Sprintf("%016x", now.UnixNano())
	containerID = containerID[len(containerID)-64:]
	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = store.pool.Exec(cleanupContext, `DELETE FROM agents WHERE id = $1`, agentID)
	}()

	cpu := 92.0
	memory := 91.0
	batch := telemetry.Batch{
		Version: telemetry.APIVersion, Agent: telemetry.Agent{ID: agentID, Hostname: "alert-host"},
		Sequence: now.UnixNano(), SentAt: now,
		Samples: []telemetry.Sample{{
			ContainerID: containerID, ContainerName: "alert-container", Image: "alert:test",
			Platform: "linux", CreatedAt: now.Add(-time.Minute), SampledAt: now,
			State: "running", Health: "healthy", CPUUtilizationPercent: &cpu,
			MemoryUtilizationPercent: &memory,
		}},
	}
	if accepted, err := store.Ingest(ctx, batch); err != nil || !accepted {
		t.Fatalf("Ingest() = (%t, %v), want (true, nil)", accepted, err)
	}

	policy := alerting.DefaultPolicy()
	signals, err := store.ListAlertSignals(ctx, now, policy)
	if err != nil {
		t.Fatal(err)
	}
	var cpuSignal, memorySignal *alerting.Signal
	for index := range signals {
		if signals[index].Fingerprint == "high_cpu:"+containerID {
			cpuSignal = &signals[index]
		}
		if signals[index].Fingerprint == "high_memory:"+containerID {
			memorySignal = &signals[index]
		}
	}
	if cpuSignal == nil || !cpuSignal.Known || !cpuSignal.Breached {
		t.Fatalf("high CPU signal = %#v", cpuSignal)
	}
	if memorySignal == nil || !memorySignal.Known || !memorySignal.Breached {
		t.Fatalf("high memory signal = %#v", memorySignal)
	}

	incident, err := store.CreateIncident(ctx, alerting.Incident{
		Fingerprint: cpuSignal.Fingerprint, Kind: cpuSignal.Kind, AgentID: agentID,
		ContainerID: containerID, State: alerting.StatePending, Title: cpuSignal.Title,
		Threshold: cpuSignal.Threshold, ObservedValue: cpuSignal.ObservedValue,
		OpenedAt: now, LastEvaluatedAt: now, LastObservedAt: now, Details: cpuSignal.Details,
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.FindActiveIncident(ctx, cpuSignal.Fingerprint)
	if err != nil || active == nil || active.ID != incident.ID {
		t.Fatalf("FindActiveIncident() = (%#v, %v)", active, err)
	}
	resolvedAt := now.Add(time.Minute)
	incident.State = alerting.StateResolved
	incident.ResolvedAt = &resolvedAt
	incident.LastEvaluatedAt = resolvedAt
	if err := store.UpdateIncident(ctx, incident); err != nil {
		t.Fatal(err)
	}
	history, err := store.ListIncidents(ctx, alerting.ViewHistory, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, stored := range history {
		if stored.ID == incident.ID && stored.State == alerting.StateResolved {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("incident history did not include %d", incident.ID)
	}
}
