package telemetry

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

var (
	agentIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	containerIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func ValidateBatch(batch Batch, now time.Time) error {
	if batch.Version != APIVersion {
		return fmt.Errorf("unsupported version %d", batch.Version)
	}
	if !agentIDPattern.MatchString(batch.Agent.ID) {
		return errors.New("agent.id must contain only letters, numbers, dots, underscores, or hyphens")
	}
	if strings.TrimSpace(batch.Agent.Hostname) == "" || len(batch.Agent.Hostname) > 255 {
		return errors.New("agent.hostname must be between 1 and 255 characters")
	}
	if batch.Sequence <= 0 {
		return errors.New("sequence must be positive")
	}
	if batch.SentAt.IsZero() || batch.SentAt.After(now.Add(2*time.Minute)) {
		return errors.New("sent_at is missing or too far in the future")
	}
	if len(batch.Samples) > MaxBatchSamples {
		return fmt.Errorf("samples exceeds maximum of %d", MaxBatchSamples)
	}
	for index, sample := range batch.Samples {
		if err := validateSample(sample, now); err != nil {
			return fmt.Errorf("samples[%d]: %w", index, err)
		}
	}
	return nil
}

func validateSample(sample Sample, now time.Time) error {
	if !containerIDPattern.MatchString(sample.ContainerID) {
		return errors.New("container_id must be a Docker hexadecimal ID")
	}
	if strings.TrimSpace(sample.ContainerName) == "" || len(sample.ContainerName) > 255 {
		return errors.New("container_name must be between 1 and 255 characters")
	}
	if len(sample.Image) > 512 || len(sample.Platform) > 32 || len(sample.State) > 32 || len(sample.Health) > 32 {
		return errors.New("one or more string fields exceed their size limit")
	}
	if sample.SampledAt.IsZero() || sample.SampledAt.After(now.Add(2*time.Minute)) {
		return errors.New("sampled_at is missing or too far in the future")
	}
	if sample.SampledAt.Before(now.Add(-8 * 24 * time.Hour)) {
		return errors.New("sampled_at is older than the ingestion window")
	}
	if sample.CreatedAt.IsZero() || sample.CreatedAt.After(now.Add(2*time.Minute)) {
		return errors.New("created_at is missing or invalid")
	}
	if len(sample.Labels) > 64 {
		return errors.New("labels exceeds maximum of 64 entries")
	}
	for key, value := range sample.Labels {
		if key == "" || len(key) > 255 || len(value) > 1024 {
			return errors.New("label key or value exceeds its size limit")
		}
	}
	if len(sample.CollectionError) > 2048 {
		return errors.New("collection_error exceeds 2048 characters")
	}
	for name, value := range map[string]*float64{
		"cpu_limit_cores":            sample.CPULimitCores,
		"cpu_used_cores":             sample.CPUUsedCores,
		"cpu_docker_percent":         sample.CPUDockerPercent,
		"cpu_utilization_percent":    sample.CPUUtilizationPercent,
		"memory_utilization_percent": sample.MemoryUtilizationPercent,
	} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return fmt.Errorf("%s must be a finite non-negative number", name)
		}
	}
	return nil
}
