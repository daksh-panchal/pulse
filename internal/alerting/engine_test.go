package alerting

import (
	"context"
	"math"
	"testing"
	"time"
)

type memoryRepository struct {
	signals   []Signal
	incidents []Incident
	nextID    int64
}

func (repository *memoryRepository) ListAlertSignals(context.Context, time.Time, Policy) ([]Signal, error) {
	return repository.signals, nil
}

func (repository *memoryRepository) FindActiveIncident(_ context.Context, fingerprint string) (*Incident, error) {
	for index := range repository.incidents {
		incident := &repository.incidents[index]
		if incident.Fingerprint == fingerprint && incident.ResolvedAt == nil {
			copy := *incident
			return &copy, nil
		}
	}
	return nil, nil
}

func (repository *memoryRepository) CreateIncident(_ context.Context, incident Incident) (Incident, error) {
	repository.nextID++
	incident.ID = repository.nextID
	repository.incidents = append(repository.incidents, incident)
	return incident, nil
}

func (repository *memoryRepository) UpdateIncident(_ context.Context, updated Incident) error {
	for index := range repository.incidents {
		if repository.incidents[index].ID == updated.ID {
			repository.incidents[index] = updated
			return nil
		}
	}
	return nil
}

func TestIncidentLifecycle(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	policy := DefaultPolicy()
	policy.ResourceSustain = 10 * time.Second
	policy.RecoveryDuration = 5 * time.Second
	value := 95.0
	threshold := 80.0
	repository := &memoryRepository{signals: []Signal{{
		Fingerprint: "high_cpu:container", Kind: KindHighCPU, AgentID: "agent",
		ContainerID: "container", Title: "High CPU", Known: true, Breached: true,
		ObservedValue: &value, Threshold: &threshold, ObservedAt: now,
	}}}
	engine, err := New(repository, policy)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := engine.Evaluate(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	assertState(t, repository, StatePending)
	if _, err := engine.Evaluate(context.Background(), now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertState(t, repository, StateFiring)

	repository.signals[0].Breached = false
	if _, err := engine.Evaluate(context.Background(), now.Add(11*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertState(t, repository, StateRecovering)
	if _, err := engine.Evaluate(context.Background(), now.Add(16*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertState(t, repository, StateResolved)
}

func TestUnknownSignalDoesNotResolveIncident(t *testing.T) {
	now := time.Now().UTC()
	repository := &memoryRepository{nextID: 1, incidents: []Incident{{
		ID: 1, Fingerprint: "high_memory:container", Kind: KindHighMemory,
		State: StateFiring, OpenedAt: now.Add(-time.Minute), LastEvaluatedAt: now,
	}}, signals: []Signal{{Fingerprint: "high_memory:container", Kind: KindHighMemory, Known: false}}}
	engine, err := New(repository, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Evaluate(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	assertState(t, repository, StateFiring)
}

func TestProblemReturningDuringRecoveryResumesFiring(t *testing.T) {
	now := time.Now().UTC()
	recoveryStartedAt := now.Add(-time.Second)
	repository := &memoryRepository{nextID: 1, incidents: []Incident{{
		ID: 1, Fingerprint: "high_cpu:container", Kind: KindHighCPU,
		State: StateRecovering, OpenedAt: now.Add(-time.Minute),
		RecoveryStartedAt: &recoveryStartedAt, LastEvaluatedAt: now,
	}}, signals: []Signal{{
		Fingerprint: "high_cpu:container", Kind: KindHighCPU, Known: true,
		Breached: true, ObservedAt: now,
	}}}
	engine, err := New(repository, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Evaluate(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	assertState(t, repository, StateFiring)
	if repository.incidents[0].RecoveryStartedAt != nil {
		t.Fatal("recovery timestamp was not cleared when the problem returned")
	}
}

func TestPolicyRejectsNonFiniteThreshold(t *testing.T) {
	policy := DefaultPolicy()
	policy.CPUThresholdPercent = math.NaN()
	if _, err := New(&memoryRepository{}, policy); err == nil {
		t.Fatal("New() accepted a non-finite CPU threshold")
	}
}

func assertState(t *testing.T, repository *memoryRepository, want State) {
	t.Helper()
	if len(repository.incidents) != 1 || repository.incidents[0].State != want {
		t.Fatalf("incidents = %#v, want state %s", repository.incidents, want)
	}
}
