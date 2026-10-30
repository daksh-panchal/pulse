package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/daksh-panchal/pulse/internal/agentclient"
	"github.com/daksh-panchal/pulse/internal/dockercollector"
	"github.com/daksh-panchal/pulse/internal/telemetry"
)

const defaultLabelFilter = "pulse.monitor=true"

var invalidAgentIDCharacter = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func main() {
	runOnce := flag.Bool("once", false, "collect and send one telemetry batch, then exit")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	interval, err := durationEnvironment("PULSE_AGENT_INTERVAL", 5*time.Second, time.Second, 5*time.Minute)
	if err != nil {
		fatal(logger, "invalid agent interval", err)
	}
	requestTimeout, err := durationEnvironment("PULSE_AGENT_REQUEST_TIMEOUT", 20*time.Second, time.Second, 2*time.Minute)
	if err != nil {
		fatal(logger, "invalid request timeout", err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		fatal(logger, "read hostname", err)
	}
	agentID := environment("PULSE_AGENT_ID", sanitizeAgentID(hostname))
	labelFilter := environment("PULSE_DOCKER_LABEL", defaultLabelFilter)
	serverURL := environment("PULSE_SERVER_URL", "http://127.0.0.1:8080")

	collector, err := dockercollector.New(labelFilter)
	if err != nil {
		fatal(logger, "create Docker collector", err)
	}
	defer collector.Close()
	sender, err := agentclient.New(serverURL, requestTimeout)
	if err != nil {
		fatal(logger, "create telemetry sender", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sequence := time.Now().UTC().UnixNano()

	collectAndSend := func() error {
		cycleContext, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		samples, err := collector.Collect(cycleContext)
		if err != nil {
			return fmt.Errorf("collect telemetry: %w", err)
		}
		batch := telemetry.Batch{
			Version:  telemetry.APIVersion,
			Agent:    telemetry.Agent{ID: agentID, Hostname: hostname},
			Sequence: sequence,
			SentAt:   time.Now().UTC(),
			Samples:  samples,
		}
		sequence++
		accepted, err := sender.Send(cycleContext, batch)
		if err != nil {
			return fmt.Errorf("send telemetry sequence %d: %w", batch.Sequence, err)
		}
		logger.Info("telemetry delivered", "sequence", batch.Sequence, "samples", len(samples), "accepted", accepted)
		return nil
	}

	if err := collectAndSend(); err != nil {
		if *runOnce {
			fatal(logger, "one-shot collection failed", err)
		}
		logger.Error("telemetry cycle failed", "error", err)
	}
	if *runOnce {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := collectAndSend(); err != nil {
				logger.Error("telemetry cycle failed", "error", err)
			}
		}
	}
}

func durationEnvironment(name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %s and %s", name, minimum, maximum)
	}
	return parsed, nil
}

func sanitizeAgentID(value string) string {
	value = strings.Trim(invalidAgentIDCharacter.ReplaceAllString(value, "-"), "-._")
	if value == "" {
		return "pulse-agent"
	}
	if len(value) > 128 {
		return value[:128]
	}
	return value
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func fatal(logger *slog.Logger, operation string, err error) {
	logger.Error(operation, "error", err)
	os.Exit(1)
}
