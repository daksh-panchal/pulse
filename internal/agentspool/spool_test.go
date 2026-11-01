package agentspool

import (
	"errors"
	"testing"
	"time"

	"github.com/daksh-panchal/pulse/internal/telemetry"
)

func TestSpoolPersistsOrdersAndRemovesBatches(t *testing.T) {
	directory := t.TempDir()
	spool, err := Open(directory, 3)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	newer := telemetry.Batch{Agent: telemetry.Agent{ID: "agent"}, Sequence: 2, SentAt: now.Add(time.Second)}
	older := telemetry.Batch{Agent: telemetry.Agent{ID: "agent"}, Sequence: 1, SentAt: now}
	if err := spool.Enqueue(newer); err != nil {
		t.Fatal(err)
	}
	if err := spool.Enqueue(older); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(directory, 3)
	if err != nil {
		t.Fatal(err)
	}
	items, err := reopened.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Batch.Sequence != 1 || items[1].Batch.Sequence != 2 {
		t.Fatalf("unexpected pending batches: %#v", items)
	}
	if err := reopened.Remove(items[0]); err != nil {
		t.Fatal(err)
	}
	items, err = reopened.Pending()
	if err != nil || len(items) != 1 || items[0].Batch.Sequence != 2 {
		t.Fatalf("pending after removal = %#v, %v", items, err)
	}
}

func TestSpoolIsBounded(t *testing.T) {
	spool, err := Open(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := spool.Enqueue(telemetry.Batch{Agent: telemetry.Agent{ID: "agent"}, Sequence: 1, SentAt: now}); err != nil {
		t.Fatal(err)
	}
	err = spool.Enqueue(telemetry.Batch{Agent: telemetry.Agent{ID: "agent"}, Sequence: 2, SentAt: now})
	if !errors.Is(err, ErrFull) {
		t.Fatalf("Enqueue() error = %v, want ErrFull", err)
	}
}

func TestNextSequenceContinuesAfterRestart(t *testing.T) {
	spool, err := Open(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := spool.Enqueue(telemetry.Batch{
		Agent: telemetry.Agent{ID: "agent"}, Sequence: 100, SentAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	next, err := spool.NextSequence(50)
	if err != nil || next != 101 {
		t.Fatalf("NextSequence() = (%d, %v), want (101, nil)", next, err)
	}
}
