package main

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
	if !ok {
		t.Fatal("calculateCPU() reported unavailable counters")
	}
	if math.Abs(cores-1) > 0.0001 || math.Abs(percent-100) > 0.0001 {
		t.Fatalf("calculateCPU() = (%f cores, %f%%), want (1, 100)", cores, percent)
	}
}

func TestCalculateCPURejectsCounterRegression(t *testing.T) {
	var stats statsPayload
	stats.PreCPUStats.CPUUsage.TotalUsage = 200
	stats.CPUStats.CPUUsage.TotalUsage = 100
	stats.PreCPUStats.SystemUsage = 1_000
	stats.CPUStats.SystemUsage = 2_000
	stats.CPUStats.OnlineCPUs = 4

	if _, _, ok := calculateCPU(stats); ok {
		t.Fatal("calculateCPU() accepted a regressed CPU counter")
	}
}

func TestEffectiveCPULimitPrecedence(t *testing.T) {
	limit, explicit, _ := effectiveCPULimit(500_000_000, 200_000, 100_000, "0-3", 8)
	if !explicit || limit != 0.5 {
		t.Fatalf("effectiveCPULimit() = (%f, %t), want (0.5, true)", limit, explicit)
	}

	limit, explicit, _ = effectiveCPULimit(0, 200_000, 100_000, "0-3", 8)
	if !explicit || limit != 2 {
		t.Fatalf("quota limit = (%f, %t), want (2, true)", limit, explicit)
	}

	limit, explicit, _ = effectiveCPULimit(0, 0, 0, "0-1,4,6-7", 8)
	if !explicit || limit != 5 {
		t.Fatalf("cpuset limit = (%f, %t), want (5, true)", limit, explicit)
	}

	limit, explicit, _ = effectiveCPULimit(0, 0, 0, "", 8)
	if explicit || limit != 8 {
		t.Fatalf("host fallback = (%f, %t), want (8, false)", limit, explicit)
	}
}

func TestCountCPUSet(t *testing.T) {
	tests := map[string]int{
		"":          0,
		"0":         1,
		"0-3":       4,
		"0-2,2-4":   5,
		"0-1,4,6-7": 5,
	}
	for input, want := range tests {
		got, err := countCPUSet(input)
		if err != nil {
			t.Fatalf("countCPUSet(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("countCPUSet(%q) = %d, want %d", input, got, want)
		}
	}

	for _, input := range []string{"-1", "3-1", "0,,2", "x", "1-2-3", "1x"} {
		if _, err := countCPUSet(input); err == nil {
			t.Errorf("countCPUSet(%q) unexpectedly succeeded", input)
		}
	}
}

func TestMemoryValues(t *testing.T) {
	var stats statsPayload
	stats.MemoryStats.Usage = 1_000
	stats.MemoryStats.Limit = 2_000
	stats.MemoryStats.Stats = map[string]uint64{"inactive_file": 250}

	raw, workingSet, limit := memoryValues(stats)
	if raw != 1_000 || workingSet != 750 || limit != 2_000 {
		t.Fatalf("memoryValues() = (%d, %d, %d), want (1000, 750, 2000)", raw, workingSet, limit)
	}
}

func TestNetworkTotals(t *testing.T) {
	rx, tx := networkTotals(map[string]networkStats{
		"eth0": {RxBytes: 10, TxBytes: 20},
		"eth1": {RxBytes: 30, TxBytes: 40},
	})
	if rx != 40 || tx != 60 {
		t.Fatalf("networkTotals() = (%d, %d), want (40, 60)", rx, tx)
	}
}
