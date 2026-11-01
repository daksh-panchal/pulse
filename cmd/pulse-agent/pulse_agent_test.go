package main

import (
	"os"
	"testing"
	"time"
)

func TestSanitizeAgentID(t *testing.T) {
	tests := map[string]string{
		"desktop":        "desktop",
		"My Workstation": "My-Workstation",
		"../../agent":    "agent",
		"***":            "pulse-agent",
	}
	for input, want := range tests {
		if got := sanitizeAgentID(input); got != want {
			t.Errorf("sanitizeAgentID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDurationEnvironment(t *testing.T) {
	const name = "PULSE_TEST_DURATION"
	t.Cleanup(func() { _ = os.Unsetenv(name) })
	if err := os.Setenv(name, "10s"); err != nil {
		t.Fatal(err)
	}
	got, err := durationEnvironment(name, time.Second, time.Second, time.Minute)
	if err != nil || got != 10*time.Second {
		t.Fatalf("durationEnvironment() = (%s, %v), want (10s, nil)", got, err)
	}
	if err := os.Setenv(name, "10m"); err != nil {
		t.Fatal(err)
	}
	if _, err := durationEnvironment(name, time.Second, time.Second, time.Minute); err == nil {
		t.Fatal("durationEnvironment() accepted a duration above its maximum")
	}
}

func TestPositiveIntEnvironment(t *testing.T) {
	t.Setenv("PULSE_TEST_INTEGER", "12")
	value, err := positiveIntEnvironment("PULSE_TEST_INTEGER", 5, 1, 20)
	if err != nil || value != 12 {
		t.Fatalf("positiveIntEnvironment() = (%d, %v), want (12, nil)", value, err)
	}
	t.Setenv("PULSE_TEST_INTEGER", "21")
	if _, err := positiveIntEnvironment("PULSE_TEST_INTEGER", 5, 1, 20); err == nil {
		t.Fatal("positiveIntEnvironment() accepted an out-of-range value")
	}
}
