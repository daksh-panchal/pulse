package dockercollector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/daksh-panchal/pulse/internal/telemetry"
	"github.com/moby/moby/client"
)

const maxStatsResponse = 8 << 20

type Collector struct {
	client      *client.Client
	labelFilter string
	clock       func() time.Time
}

func New(labelFilter string) (*Collector, error) { // Create collector
	if strings.TrimSpace(labelFilter) == "" {
		return nil, errors.New("Docker label filter must not be empty")
	}
	apiClient, err := client.New(
		client.FromEnv,
		client.WithUserAgent("pulse-agent/0.1"),
	)
	if err != nil {
		return nil, fmt.Errorf("create Docker client: %w", err)
	}
	return &Collector{client: apiClient, labelFilter: labelFilter, clock: func() time.Time { return time.Now().UTC() }}, nil
}

func (collector *Collector) Close() error { return collector.client.Close() } // Release resources held by Docker client on shutdown

func (collector *Collector) Collect(ctx context.Context) ([]telemetry.Sample, error) {
	if _, err := collector.client.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		return nil, fmt.Errorf("ping Docker Engine: %w", err)
	}
	filter := make(client.Filters).Add("label", collector.labelFilter)
	listed, err := collector.client.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filter})
	if err != nil {
		return nil, fmt.Errorf("list monitored containers: %w", err)
	}

	// Iterate through discovered containers
	samples := make([]telemetry.Sample, 0, len(listed.Items))
	for _, summary := range listed.Items {
		sample, err := collector.collectContainer(ctx, summary.ID, summary.Image, summary.Labels)
		if err != nil {
			if sample.ContainerID == "" {
				sample.ContainerID = summary.ID
			}
			if sample.ContainerName == "" && len(summary.Names) > 0 {
				sample.ContainerName = strings.TrimPrefix(summary.Names[0], "/")
			}
			if sample.ContainerName == "" {
				sample.ContainerName = summary.ID[:min(12, len(summary.ID))]
			}
			sample.Image = summary.Image
			sample.State = string(summary.State)
			sample.Health = "not-configured"
			if summary.Health != nil {
				sample.Health = string(summary.Health.Status)
			}
			sample.Labels = copySafeLabels(summary.Labels)
			sample.SampledAt = collector.clock()
			sample.CollectionError = truncate(err.Error(), 2048)
		}
		samples = append(samples, sample)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].ContainerName < samples[j].ContainerName })
	return samples, nil
}

func (collector *Collector) collectContainer(ctx context.Context, id, image string, labels map[string]string) (telemetry.Sample, error) {
	inspected, err := collector.client.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return telemetry.Sample{}, fmt.Errorf("inspect %s: %w", id, err)
	}
	c := inspected.Container
	createdAt, err := time.Parse(time.RFC3339Nano, c.Created)
	if err != nil {
		return telemetry.Sample{ContainerID: c.ID, ContainerName: strings.TrimPrefix(c.Name, "/")}, fmt.Errorf("parse creation time: %w", err)
	}
	sample := telemetry.Sample{
		ContainerID:   c.ID,
		ContainerName: strings.TrimPrefix(c.Name, "/"),
		Image:         image,
		Platform:      c.Platform,
		CreatedAt:     createdAt.UTC(),
		SampledAt:     collector.clock(),
		State:         "unknown",
		Health:        "not-configured",
		Labels:        copySafeLabels(labels),
	}
	if c.Config != nil {
		sample.Labels = copySafeLabels(c.Config.Labels)
	}
	if c.HostConfig != nil {
		if limit := configuredCPULimit(c.HostConfig.NanoCPUs, c.HostConfig.CPUQuota, c.HostConfig.CPUPeriod, c.HostConfig.CpusetCpus); limit > 0 {
			sample.CPULimitCores = floatPointer(limit)
		}
		if c.HostConfig.Memory > 0 {
			sample.MemoryLimitBytes = uintPointer(uint64(c.HostConfig.Memory))
		}
	}
	if c.State == nil {
		return sample, errors.New("Docker inspect did not include container state")
	}
	sample.State = string(c.State.Status)
	if c.State.Health != nil {
		sample.Health = string(c.State.Health.Status)
	}
	if !c.State.Running {
		return sample, nil
	}

	result, err := collector.client.ContainerStats(ctx, id, client.ContainerStatsOptions{
		Stream: false, IncludePreviousSample: true,
	})
	if err != nil {
		return sample, fmt.Errorf("collect stats: %w", err)
	}
	defer result.Body.Close()
	stats, err := decodeStats(result.Body)
	if err != nil {
		return sample, fmt.Errorf("decode stats: %w", err)
	}
	if !stats.Read.IsZero() {
		sample.SampledAt = stats.Read.UTC()
	}

	if strings.EqualFold(c.Platform, "linux") || c.Platform == "" {
		if usedCores, dockerPercent, ok := calculateCPU(stats); ok {
			sample.CPUUsedCores = floatPointer(usedCores)
			sample.CPUDockerPercent = floatPointer(dockerPercent)
			limit := 0.0
			if sample.CPULimitCores != nil {
				limit = *sample.CPULimitCores
			} else if stats.CPUStats.OnlineCPUs > 0 {
				limit = float64(stats.CPUStats.OnlineCPUs)
				sample.CPULimitCores = floatPointer(limit)
			}
			if limit > 0 {
				sample.CPUUtilizationPercent = floatPointer(usedCores / limit * 100)
			}
		}
	} else {
		sample.CollectionError = "CPU utilization is not yet supported for Windows containers"
	}

	raw, workingSet, memoryLimit := memoryValues(stats)
	sample.MemoryRawBytes = uintPointer(raw)
	sample.MemoryWorkingSetBytes = uintPointer(workingSet)
	sample.MemoryLimitBytes = uintPointer(memoryLimit)
	if memoryLimit > 0 {
		sample.MemoryUtilizationPercent = floatPointer(float64(workingSet) / float64(memoryLimit) * 100)
	}
	rx, tx := networkTotals(stats.Networks)
	sample.NetworkRxBytes = uintPointer(rx)
	sample.NetworkTxBytes = uintPointer(tx)
	return sample, nil
}

type statsPayload struct {
	Read        time.Time `json:"read"`
	CPUStats    cpuStats  `json:"cpu_stats"`
	PreCPUStats cpuStats  `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]networkStats `json:"networks"`
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	SystemUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs  uint32 `json:"online_cpus"`
}

type networkStats struct {
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

func decodeStats(reader io.Reader) (statsPayload, error) {
	var payload statsPayload
	decoder := json.NewDecoder(io.LimitReader(reader, maxStatsResponse))
	if err := decoder.Decode(&payload); err != nil {
		return statsPayload{}, err
	}
	return payload, nil
}

func calculateCPU(stats statsPayload) (usedCores, dockerPercent float64, ok bool) {
	if stats.CPUStats.CPUUsage.TotalUsage <= stats.PreCPUStats.CPUUsage.TotalUsage ||
		stats.CPUStats.SystemUsage <= stats.PreCPUStats.SystemUsage {
		return 0, 0, false
	}
	onlineCPUs := stats.CPUStats.OnlineCPUs
	if onlineCPUs == 0 {
		onlineCPUs = stats.PreCPUStats.OnlineCPUs
	}
	if onlineCPUs == 0 {
		return 0, 0, false
	}
	containerDelta := stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage
	systemDelta := stats.CPUStats.SystemUsage - stats.PreCPUStats.SystemUsage
	usedCores = float64(containerDelta) / float64(systemDelta) * float64(onlineCPUs)
	return usedCores, usedCores * 100, true
}

func configuredCPULimit(nanoCPUs, quota, period int64, cpuset string) float64 {
	if nanoCPUs > 0 {
		return float64(nanoCPUs) / 1e9
	}
	if quota > 0 && period > 0 {
		return float64(quota) / float64(period)
	}
	count, err := countCPUSet(cpuset)
	if err == nil && count > 0 {
		return float64(count)
	}
	return 0
}

func countCPUSet(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	seen := make(map[int]struct{})
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, errors.New("empty cpuset segment")
		}
		bounds := strings.Split(part, "-")
		if len(bounds) > 2 {
			return 0, fmt.Errorf("invalid cpuset segment %q", part)
		}
		start, err := strconv.Atoi(bounds[0])
		if err != nil || start < 0 {
			return 0, fmt.Errorf("invalid CPU %q", part)
		}
		end := start
		if len(bounds) == 2 {
			end, err = strconv.Atoi(bounds[1])
			if err != nil || end < start || end-start > 65_536 {
				return 0, fmt.Errorf("invalid CPU range %q", part)
			}
		}
		for cpu := start; cpu <= end; cpu++ {
			seen[cpu] = struct{}{}
		}
	}
	return len(seen), nil
}

func memoryValues(stats statsPayload) (raw, workingSet, limit uint64) {
	raw = stats.MemoryStats.Usage
	workingSet = raw
	cache := stats.MemoryStats.Stats["inactive_file"]
	if cache == 0 {
		cache = stats.MemoryStats.Stats["total_inactive_file"]
	}
	if cache < workingSet {
		workingSet -= cache
	}
	return raw, workingSet, stats.MemoryStats.Limit
}

func networkTotals(networks map[string]networkStats) (rx, tx uint64) {
	for _, network := range networks {
		rx += network.RxBytes
		tx += network.TxBytes
	}
	return rx, tx
}

func copySafeLabels(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string)
	for key, value := range source {
		if key == "pulse.monitor" || strings.HasPrefix(key, "com.pulse.") ||
			key == "com.docker.compose.project" || key == "com.docker.compose.service" ||
			key == "com.docker.compose.container-number" {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func floatPointer(value float64) *float64 { return &value }
func uintPointer(value uint64) *uint64    { return &value }
