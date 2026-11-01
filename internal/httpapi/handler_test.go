package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daksh-panchal/pulse/internal/alerting"
	"github.com/daksh-panchal/pulse/internal/telemetry"
)

type fakeStore struct {
	accepted  bool
	batch     telemetry.Batch
	current   []telemetry.StoredSample
	metrics   []telemetry.StoredSample
	incidents []alerting.Incident
}

func (store *fakeStore) Ping(context.Context) error { return nil }
func (store *fakeStore) Close()                     {}
func (store *fakeStore) Ingest(_ context.Context, batch telemetry.Batch) (bool, error) {
	store.batch = batch
	return store.accepted, nil
}
func (store *fakeStore) ListCurrentContainers(context.Context) ([]telemetry.StoredSample, error) {
	return store.current, nil
}
func (store *fakeStore) ListMetrics(context.Context, string, time.Time, time.Time, int) ([]telemetry.StoredSample, error) {
	return store.metrics, nil
}
func (store *fakeStore) ListIncidents(context.Context, alerting.View, int) ([]alerting.Incident, error) {
	return store.incidents, nil
}

func testHandler(store *fakeStore, now time.Time) http.Handler {
	handler := &Handler{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), now: func() time.Time { return now }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.health)
	mux.HandleFunc("POST /api/v1/telemetry/batches", handler.ingest)
	mux.HandleFunc("GET /api/v1/containers", handler.listContainers)
	mux.HandleFunc("GET /api/v1/containers/{containerID}/metrics", handler.listMetrics)
	mux.HandleFunc("GET /api/v1/incidents", handler.listIncidents)
	return securityHeaders(mux)
}

func TestIngestAcceptsValidBatch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := &fakeStore{accepted: true}
	handler := testHandler(store, now)
	payload := telemetry.Batch{
		Version:  telemetry.APIVersion,
		Agent:    telemetry.Agent{ID: "test-agent", Hostname: "test-host"},
		Sequence: 42,
		SentAt:   now,
		Samples: []telemetry.Sample{{
			ContainerID: strings.Repeat("a", 64), ContainerName: "api", Image: "api:test",
			CreatedAt: now.Add(-time.Minute), SampledAt: now, State: "running", Health: "healthy",
		}},
	}
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/telemetry/batches", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if store.batch.Sequence != 42 {
		t.Fatalf("stored sequence = %d, want 42", store.batch.Sequence)
	}
}

func TestIngestRejectsUnknownFields(t *testing.T) {
	handler := testHandler(&fakeStore{}, time.Now().UTC())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/telemetry/batches", strings.NewReader(`{"unknown":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestListContainersReportsFreshness(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{current: []telemetry.StoredSample{{
		Sample: telemetry.Sample{
			ContainerID: strings.Repeat("b", 64), ContainerName: "db",
			SampledAt: now.Add(-20 * time.Second),
		},
		ReceivedAt: now,
	}}}
	handler := testHandler(store, now)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"freshness":"delayed"`) {
		t.Fatalf("body does not contain delayed freshness: %s", response.Body.String())
	}
}

func TestFreshnessBoundaries(t *testing.T) {
	tests := []struct {
		age  time.Duration
		want string
	}{
		{age: 5 * time.Second, want: "current"},
		{age: 20 * time.Second, want: "delayed"},
		{age: 31 * time.Second, want: "stale"},
	}
	for _, test := range tests {
		if got := freshness(test.age); got != test.want {
			t.Errorf("freshness(%s) = %q, want %q", test.age, got, test.want)
		}
	}
}

func TestListMetricsRejectsAbbreviatedContainerID(t *testing.T) {
	handler := testHandler(&fakeStore{}, time.Now().UTC())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/containers/abc123def456/metrics", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestListIncidentsDefaultsToActiveView(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{incidents: []alerting.Incident{{
		ID: 1, Fingerprint: "agent_stale:test", Kind: alerting.KindAgentStale,
		AgentID: "test", State: alerting.StateFiring, Title: "Agent stale",
		OpenedAt: now, LastEvaluatedAt: now, LastObservedAt: now,
	}}}
	handler := testHandler(store, now)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"view":"active"`) ||
		!strings.Contains(response.Body.String(), `"kind":"agent_stale"`) {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}
