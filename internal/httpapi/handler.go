package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/daksh-panchal/pulse/internal/alerting"
	"github.com/daksh-panchal/pulse/internal/storage"
	"github.com/daksh-panchal/pulse/internal/telemetry"
)

const (
	maxRequestBody = 1 << 20
	maxQueryRange  = 7 * 24 * time.Hour
	maxQueryLimit  = 5000
)

var dockerIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Handler struct {
	store  storage.Store
	logger *slog.Logger
	now    func() time.Time
}

func New(store storage.Store, logger *slog.Logger) http.Handler {
	handler := &Handler{store: store, logger: logger, now: func() time.Time { return time.Now().UTC() }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.health)
	mux.HandleFunc("POST /api/v1/telemetry/batches", handler.ingest)
	mux.HandleFunc("GET /api/v1/containers", handler.listContainers)
	mux.HandleFunc("GET /api/v1/containers/{containerID}/metrics", handler.listMetrics)
	mux.HandleFunc("GET /api/v1/incidents", handler.listIncidents)
	return securityHeaders(mux)
}

func (handler *Handler) health(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := handler.store.Ping(ctx); err != nil {
		handler.logger.Error("health check failed", "error", err)
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (handler *Handler) ingest(response http.ResponseWriter, request *http.Request) {
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxRequestBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var batch telemetry.Batch
	if err := decoder.Decode(&batch); err != nil {
		writeError(response, http.StatusBadRequest, "invalid JSON payload: "+safeDecodeError(err))
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := telemetry.ValidateBatch(batch, handler.now()); err != nil {
		writeError(response, http.StatusUnprocessableEntity, err.Error())
		return
	}

	accepted, err := handler.store.Ingest(request.Context(), batch)
	if err != nil {
		handler.logger.Error("ingestion failed", "agent_id", batch.Agent.ID, "sequence", batch.Sequence, "error", err)
		writeError(response, http.StatusInternalServerError, "telemetry ingestion failed")
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{
		"accepted":  accepted,
		"duplicate": !accepted,
	})
}

func (handler *Handler) listContainers(response http.ResponseWriter, request *http.Request) {
	samples, err := handler.store.ListCurrentContainers(request.Context())
	if err != nil {
		handler.logger.Error("list containers failed", "error", err)
		writeError(response, http.StatusInternalServerError, "could not query containers")
		return
	}
	now := handler.now()
	containers := make([]telemetry.CurrentContainer, 0, len(samples))
	for _, sample := range samples {
		containers = append(containers, telemetry.CurrentContainer{
			StoredSample: sample,
			Freshness:    freshness(now.Sub(sample.SampledAt)),
		})
	}
	sort.Slice(containers, func(i, j int) bool {
		return containers[i].ContainerName < containers[j].ContainerName
	})
	writeJSON(response, http.StatusOK, map[string]any{
		"generated_at": now,
		"containers":   containers,
	})
}

func (handler *Handler) listMetrics(response http.ResponseWriter, request *http.Request) {
	containerID := request.PathValue("containerID")
	if !dockerIDPattern.MatchString(containerID) {
		writeError(response, http.StatusBadRequest, "containerID must be a full 64-character Docker hexadecimal ID")
		return
	}

	now := handler.now()
	to, err := parseTime(request.URL.Query().Get("to"), now)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid to: "+err.Error())
		return
	}
	from, err := parseTime(request.URL.Query().Get("from"), to.Add(-time.Hour))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid from: "+err.Error())
		return
	}
	if !from.Before(to) {
		writeError(response, http.StatusBadRequest, "from must be before to")
		return
	}
	if to.Sub(from) > maxQueryRange {
		writeError(response, http.StatusBadRequest, "query range cannot exceed 7 days")
		return
	}
	limit, err := parseLimit(request.URL.Query().Get("limit"))
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	samples, err := handler.store.ListMetrics(request.Context(), containerID, from, to, limit)
	if err != nil {
		handler.logger.Error("list metrics failed", "container_id", containerID, "error", err)
		writeError(response, http.StatusInternalServerError, "could not query metrics")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"container_id": containerID,
		"from":         from,
		"to":           to,
		"samples":      samples,
	})
}

func (handler *Handler) listIncidents(response http.ResponseWriter, request *http.Request) {
	view := alerting.View(request.URL.Query().Get("view"))
	if view == "" {
		view = alerting.ViewActive
	}
	if view != alerting.ViewActive && view != alerting.ViewHistory && view != alerting.ViewAll {
		writeError(response, http.StatusBadRequest, "view must be active, history, or all")
		return
	}
	limit, err := parseIncidentLimit(request.URL.Query().Get("limit"))
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	incidents, err := handler.store.ListIncidents(request.Context(), view, limit)
	if err != nil {
		handler.logger.Error("list incidents failed", "view", view, "error", err)
		writeError(response, http.StatusInternalServerError, "could not query incidents")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"generated_at": handler.now(),
		"view":         view,
		"incidents":    incidents,
	})
}

func freshness(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	switch {
	case age <= 15*time.Second:
		return "current"
	case age <= 30*time.Second:
		return "delayed"
	default:
		return "stale"
	}
}

func parseTime(value string, fallback time.Time) (time.Time, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, errors.New("must use RFC3339 format")
	}
	return parsed.UTC(), nil
}

func parseLimit(value string) (int, error) {
	if value == "" {
		return 1000, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > maxQueryLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d", maxQueryLimit)
	}
	return limit, nil
}

func parseIncidentLimit(value string) (int, error) {
	if value == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 1000 {
		return 0, errors.New("limit must be between 1 and 1000")
	}
	return limit, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}

func safeDecodeError(err error) string {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return "request body exceeds 1 MiB"
	}
	return err.Error()
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(response, request)
	})
}
