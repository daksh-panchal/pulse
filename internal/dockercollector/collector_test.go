package dockercollector

import (
	"math"
	"testing"
)

func TestCalculateCPU(t *testing.T) {
	var stats statsPayload
	stats.PreCPUStats.CPUUsage.TotalUsage = 1_000
	stats.CPUStats.CPUUsage.TotalUsage = 1_200
	stats.PreCPUStats.SystemUsage = 10_000
	stats.CPUStats.SystemUsage = 10_800
	stats.CPUStats.OnlineCPUs = 4
	cores, percent, ok := calculateCPU(stats)
	if !ok || math.Abs(cores-1) > 0.0001 || math.Abs(percent-100) > 0.0001 {
		t.Fatalf("calculateCPU() = (%f, %f, %t), want (1, 100, true)", cores, percent, ok)
	}
}

func TestConfiguredCPULimitPrecedence(t *testing.T) {
	if got := configuredCPULimit(500_000_000, 200_000, 100_000, "0-3"); got != 0.5 {
		t.Fatalf("NanoCPU limit = %f, want 0.5", got)
	}
	if got := configuredCPULimit(0, 200_000, 100_000, "0-3"); got != 2 {
		t.Fatalf("quota limit = %f, want 2", got)
	}
	if got := configuredCPULimit(0, 0, 0, "0-1,4"); got != 3 {
		t.Fatalf("cpuset limit = %f, want 3", got)
	}
}

func TestMemoryAndNetworkValues(t *testing.T) {
	var stats statsPayload
	stats.MemoryStats.Usage = 1000
	stats.MemoryStats.Limit = 2000
	stats.MemoryStats.Stats = map[string]uint64{"inactive_file": 250}
	stats.Networks = map[string]networkStats{
		"eth0": {RxBytes: 10, TxBytes: 20},
		"eth1": {RxBytes: 30, TxBytes: 40},
	}
	raw, working, limit := memoryValues(stats)
	if raw != 1000 || working != 750 || limit != 2000 {
		t.Fatalf("memoryValues() = (%d, %d, %d)", raw, working, limit)
	}
	rx, tx := networkTotals(stats.Networks)
	if rx != 40 || tx != 60 {
		t.Fatalf("networkTotals() = (%d, %d), want (40, 60)", rx, tx)
	}
}

func TestCopySafeLabelsExcludesHostPathsAndUnrelatedLabels(t *testing.T) {
	labels := copySafeLabels(map[string]string{
		"pulse.monitor":                           "true",
		"com.pulse.owner":                         "demo",
		"com.docker.compose.project":              "pulse-demo",
		"com.docker.compose.project.config_files": `C:\\Users\\person\\project\\compose.yaml`,
		"unrelated.secret":                        "do-not-send",
	})
	if len(labels) != 3 {
		t.Fatalf("copySafeLabels() returned %#v", labels)
	}
	if _, exists := labels["com.docker.compose.project.config_files"]; exists {
		t.Fatal("copySafeLabels() retained a host config path")
	}
	if _, exists := labels["unrelated.secret"]; exists {
		t.Fatal("copySafeLabels() retained an unrelated label")
	}
}
