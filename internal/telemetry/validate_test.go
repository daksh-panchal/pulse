package telemetry

import (
	"strings"
	"testing"
	"time"
)

func validBatch(now time.Time) Batch {
	value := 1.0
	return Batch{
		Version:  APIVersion,
		Agent:    Agent{ID: "desktop-agent", Hostname: "desktop"},
		Sequence: 1,
		SentAt:   now,
		Samples: []Sample{{
			ContainerID:           strings.Repeat("a", 64),
			ContainerName:         "api",
			Image:                 "example/api:dev",
			Platform:              "linux",
			CreatedAt:             now.Add(-time.Minute),
			SampledAt:             now,
			State:                 "running",
			Health:                "healthy",
			CPUUtilizationPercent: &value,
		}},
	}
}

func TestValidateBatch(t *testing.T) {
	now := time.Now().UTC()
	if err := ValidateBatch(validBatch(now), now); err != nil {
		t.Fatalf("ValidateBatch() returned %v", err)
	}
}

func TestValidateBatchRejectsFutureSample(t *testing.T) {
	now := time.Now().UTC()
	batch := validBatch(now)
	batch.Samples[0].SampledAt = now.Add(3 * time.Minute)
	if err := ValidateBatch(batch, now); err == nil {
		t.Fatal("ValidateBatch() accepted a future sample")
	}
}

func TestValidateBatchRejectsUnknownVersion(t *testing.T) {
	now := time.Now().UTC()
	batch := validBatch(now)
	batch.Version = APIVersion + 1
	if err := ValidateBatch(batch, now); err == nil {
		t.Fatal("ValidateBatch() accepted an unknown version")
	}
}
