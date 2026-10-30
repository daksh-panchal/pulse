package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/daksh-panchal/pulse/internal/telemetry"
)

type Sender struct {
	endpoint string
	client   *http.Client
}

func New(serverURL string, timeout time.Duration) (*Sender, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("server URL must be an absolute http or https URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/v1/telemetry/batches"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return &Sender{
		endpoint: parsed.String(),
		client:   &http.Client{Timeout: timeout},
	}, nil
}

func (sender *Sender) Send(ctx context.Context, batch telemetry.Batch) (bool, error) {
	body, err := json.Marshal(batch)
	if err != nil {
		return false, fmt.Errorf("encode telemetry batch: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, sender.endpoint, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("create ingestion request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "pulse-agent/0.1")
	response, err := sender.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("send telemetry batch: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return false, fmt.Errorf("read ingestion response: %w", err)
	}
	if response.StatusCode != http.StatusAccepted {
		return false, fmt.Errorf("ingestion returned %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	var result struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return false, fmt.Errorf("decode ingestion response: %w", err)
	}
	return result.Accepted, nil
}
