package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

const defaultLabel = "pulse.monitor=true"

type statsPayload struct {
	Read        time.Time `json:"read"`
	PreRead     time.Time `json:"preread"`
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

type output struct {
	CollectedAt time.Time         `json:"collected_at"`
	Filter      string            `json:"filter"`
	Containers  []containerOutput `json:"containers"`
}

type containerOutput struct {
	ID                       string   `json:"id"`
	Name                     string   `json:"name"`
	Image                    string   `json:"image"`
	Platform                 string   `json:"platform,omitempty"`
	State                    string   `json:"state"`
	Health                   string   `json:"health"`
	CreatedAt                string   `json:"created_at,omitempty"`
	CPULimitCores            *float64 `json:"cpu_limit_cores,omitempty"`
	CPUUsedCores             *float64 `json:"cpu_used_cores,omitempty"`
	CPUDockerPercent         *float64 `json:"cpu_docker_percent,omitempty"`
	CPUUtilizationPercent    *float64 `json:"cpu_utilization_percent,omitempty"`
	MemoryRawBytes           *uint64  `json:"memory_raw_bytes,omitempty"`
	MemoryWorkingSetBytes    *uint64  `json:"memory_working_set_bytes,omitempty"`
	MemoryLimitBytes         *uint64  `json:"memory_limit_bytes,omitempty"`
	MemoryUtilizationPercent *float64 `json:"memory_utilization_percent,omitempty"`
	NetworkRxBytes           *uint64  `json:"network_rx_bytes,omitempty"`
	NetworkTxBytes           *uint64  `json:"network_tx_bytes,omitempty"`
	Notes                    []string `json:"notes,omitempty"`
}

func main() {
	label := flag.String("label", defaultLabel, "Docker label filter; key=value is recommended")
	timeout := flag.Duration("timeout", 20*time.Second, "overall Docker request timeout")
	flag.Parse()

	if strings.TrimSpace(*label) == "" {
		slog.Error("label filter must not be empty; refusing unscoped discovery")
		os.Exit(2)
	}
	if *timeout <= 0 || *timeout > 2*time.Minute {
		slog.Error("timeout must be greater than zero and at most two minutes")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	apiClient, err := client.New(
		client.FromEnv,
		client.WithUserAgent("pulse-docker-telemetry-spike/0.1"),
	)
	if err != nil {
		fatal("create Docker client", err)
	}
	defer apiClient.Close()

	if _, err := apiClient.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		fatal("ping Docker Engine (is Docker Desktop running and DOCKER_HOST correct?)", err)
	}

	filter := make(client.Filters).Add("label", *label)
	listed, err := apiClient.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: filter,
	})
	if err != nil {
		fatal("list opted-in containers", err)
	}

	result := output{
		CollectedAt: time.Now().UTC(),
		Filter:      *label,
		Containers:  make([]containerOutput, 0, len(listed.Items)),
	}

	for _, summary := range listed.Items {
		item, err := collectContainer(ctx, apiClient, summary.ID, summary.Image)
		if err != nil {
			item.ID = summary.ID
			item.Image = summary.Image
			item.State = string(summary.State)
			item.Health = "unknown"
			item.Notes = append(item.Notes, "collection error: "+err.Error())
		}
		result.Containers = append(result.Containers, item)
	}

	sort.Slice(result.Containers, func(i, j int) bool {
		return result.Containers[i].Name < result.Containers[j].Name
	})

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fatal("encode output", err)
	}
}

func collectContainer(ctx context.Context, apiClient *client.Client, id, image string) (containerOutput, error) {
	inspected, err := apiClient.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return containerOutput{}, fmt.Errorf("inspect: %w", err)
	}

	c := inspected.Container
	item := containerOutput{
		ID:        c.ID,
		Name:      strings.TrimPrefix(c.Name, "/"),
		Image:     image,
		Platform:  c.Platform,
		CreatedAt: c.Created,
		Health:    "not-configured",
	}

	if c.State == nil {
		item.State = "unknown"
		item.Notes = append(item.Notes, "Docker inspect did not include container state")
		return item, nil
	}
	item.State = string(c.State.Status)
	if c.State.Health != nil {
		item.Health = string(c.State.Health.Status)
	}
	if !c.State.Running {
		item.Notes = append(item.Notes, "container is not running; stats were not requested")
		return item, nil
	}

	statsResult, err := apiClient.ContainerStats(ctx, id, client.ContainerStatsOptions{
		Stream:                false,
		IncludePreviousSample: true,
	})
	if err != nil {
		return item, fmt.Errorf("stats: %w", err)
	}
	defer statsResult.Body.Close()

	payload, err := decodeStats(statsResult.Body)
	if err != nil {
		return item, fmt.Errorf("decode stats: %w", err)
	}

	if !strings.EqualFold(c.Platform, "linux") && c.Platform != "" {
		item.Notes = append(item.Notes, "CPU formula is only validated for Linux containers")
	} else {
		usedCores, dockerPercent, ok := calculateCPU(payload)
		if ok {
			item.CPUUsedCores = floatPointer(usedCores)
			item.CPUDockerPercent = floatPointer(dockerPercent)
		} else {
			item.Notes = append(item.Notes, "CPU utilization unavailable: missing or non-increasing counters")
		}
	}

	if c.HostConfig == nil {
		item.Notes = append(item.Notes, "Docker inspect did not include host resource configuration")
	} else {
		limit, limited, limitNote := effectiveCPULimit(
			c.HostConfig.NanoCPUs,
			c.HostConfig.CPUQuota,
			c.HostConfig.CPUPeriod,
			c.HostConfig.CpusetCpus,
			payload.CPUStats.OnlineCPUs,
		)
		if limit > 0 {
			item.CPULimitCores = floatPointer(limit)
			if item.CPUUsedCores != nil {
				item.CPUUtilizationPercent = floatPointer(*item.CPUUsedCores / limit * 100)
			}
		}
		if !limited {
			item.Notes = append(item.Notes, limitNote)
		}
	}

	raw, workingSet, limit := memoryValues(payload)
	item.MemoryRawBytes = uintPointer(raw)
	item.MemoryWorkingSetBytes = uintPointer(workingSet)
	item.MemoryLimitBytes = uintPointer(limit)
	if limit > 0 {
		item.MemoryUtilizationPercent = floatPointer(float64(workingSet) / float64(limit) * 100)
	}
	if c.HostConfig != nil && c.HostConfig.Memory <= 0 {
		item.Notes = append(item.Notes, "no explicit memory limit; reported limit may be host/VM memory")
	}

	rx, tx := networkTotals(payload.Networks)
	item.NetworkRxBytes = uintPointer(rx)
	item.NetworkTxBytes = uintPointer(tx)

	return item, nil
}

func decodeStats(reader io.Reader) (statsPayload, error) {
	var payload statsPayload
	decoder := json.NewDecoder(io.LimitReader(reader, 8<<20))
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

func effectiveCPULimit(nanoCPUs, quota, period int64, cpuset string, onlineCPUs uint32) (limit float64, explicitlyLimited bool, note string) {
	if nanoCPUs > 0 {
		return float64(nanoCPUs) / 1e9, true, ""
	}
	if quota > 0 && period > 0 {
		return float64(quota) / float64(period), true, ""
	}
	if count, err := countCPUSet(cpuset); err == nil && count > 0 {
		return float64(count), true, ""
	} else if err != nil {
		note = "could not parse cpuset: " + err.Error()
	}
	if onlineCPUs > 0 {
		if note == "" {
			note = "no explicit CPU limit; utilization is relative to all online CPUs"
		}
		return float64(onlineCPUs), false, note
	}
	if note == "" {
		note = "no explicit CPU limit and online CPU count is unavailable"
	}
	return 0, false, note
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

		var start, end int
		if strings.Contains(part, "-") {
			bounds := strings.Split(part, "-")
			if len(bounds) != 2 {
				return 0, fmt.Errorf("invalid range %q", part)
			}
			var err error
			start, err = strconv.Atoi(bounds[0])
			if err != nil {
				return 0, fmt.Errorf("invalid range %q", part)
			}
			end, err = strconv.Atoi(bounds[1])
			if err != nil || start < 0 || end < start {
				return 0, fmt.Errorf("invalid range %q", part)
			}
		} else {
			var err error
			start, err = strconv.Atoi(part)
			if err != nil || start < 0 {
				return 0, fmt.Errorf("invalid CPU %q", part)
			}
			end = start
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

func floatPointer(value float64) *float64 { return &value }
func uintPointer(value uint64) *uint64    { return &value }

func fatal(operation string, err error) {
	slog.Error(operation, "error", err)
	os.Exit(1)
}
