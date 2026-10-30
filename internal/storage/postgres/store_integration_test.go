package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/daksh-panchal/pulse/internal/telemetry"
)

func TestStoreIngestAndQuery(t *testing.T) {
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
	agentID := fmt.Sprintf("integration-%d", now.UnixNano())
	containerID := strings.Repeat("c", 48) + fmt.Sprintf("%016x", now.UnixNano())
	containerID = containerID[len(containerID)-64:]
	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = store.pool.Exec(cleanupContext, `DELETE FROM agents WHERE id = $1`, agentID)
	}()

	cpu := 25.0
	memory := uint64(1024)
	batch := telemetry.Batch{
		Version:  telemetry.APIVersion,
		Agent:    telemetry.Agent{ID: agentID, Hostname: "integration-host"},
		Sequence: now.UnixNano(),
		SentAt:   now,
		Samples: []telemetry.Sample{{
			ContainerID: containerID, ContainerName: "integration-container",
			Image: "integration:test", Platform: "linux", CreatedAt: now.Add(-time.Minute),
			SampledAt: now, State: "running", Health: "healthy",
			CPUUtilizationPercent: &cpu, MemoryWorkingSetBytes: &memory,
		}},
	}
	accepted, err := store.Ingest(ctx, batch)
	if err != nil || !accepted {
		t.Fatalf("Ingest() = (%t, %v), want (true, nil)", accepted, err)
	}
	accepted, err = store.Ingest(ctx, batch)
	if err != nil || accepted {
		t.Fatalf("duplicate Ingest() = (%t, %v), want (false, nil)", accepted, err)
	}

	metrics, err := store.ListMetrics(ctx, containerID, now.Add(-time.Second), now.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics[0].CPUUtilizationPercent == nil || *metrics[0].CPUUtilizationPercent != cpu {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
	current, err := store.ListCurrentContainers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sample := range current {
		if sample.ContainerID == containerID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("current containers did not include %s", containerID)
	}
}
