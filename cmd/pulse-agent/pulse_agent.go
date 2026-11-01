package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/daksh-panchal/pulse/internal/agentclient"
	"github.com/daksh-panchal/pulse/internal/agentspool"
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
	agentID := sanitizeAgentID(environment("PULSE_AGENT_ID", hostname))
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
	cacheDirectory, err := os.UserCacheDir()
	if err != nil {
		fatal(logger, "locate user cache", err)
	}
	spoolDirectory := environment("PULSE_AGENT_SPOOL_DIR", filepath.Join(cacheDirectory, "Pulse", "agent-spool", agentID))
	spoolMaximum, err := positiveIntEnvironment("PULSE_AGENT_SPOOL_MAX_BATCHES", 256, 1, 10_000)
	if err != nil {
		fatal(logger, "invalid spool maximum", err)
	}
	spool, err := agentspool.Open(spoolDirectory, spoolMaximum)
	if err != nil {
		fatal(logger, "open telemetry spool", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sequence, err := spool.NextSequence(time.Now().UTC().UnixNano())
	if err != nil {
		fatal(logger, "read telemetry spool", err)
	}

	collectAndQueue := func() error {
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
		if err := spool.Enqueue(batch); err != nil {
			return fmt.Errorf("queue telemetry sequence %d: %w", batch.Sequence, err)
		}
		sequence++
		return nil
	}
	flushQueue := func() error {
		items, err := spool.Pending()
		if err != nil {
			return err
		}
		for _, item := range items {
			requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
			accepted, sendErr := sender.Send(requestContext, item.Batch)
			cancel()
			if sendErr != nil {
				return fmt.Errorf("send queued telemetry sequence %d: %w", item.Batch.Sequence, sendErr)
			}
			if err := spool.Remove(item); err != nil {
				return err
			}
			logger.Info("telemetry delivered", "sequence", item.Batch.Sequence,
				"samples", len(item.Batch.Samples), "accepted", accepted)
		}
		return nil
	}
	runCycle := func() error {
		collectionErr := collectAndQueue()
		deliveryErr := flushQueue()
		return errors.Join(collectionErr, deliveryErr)
	}

	if err := runCycle(); err != nil {
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
			if err := runCycle(); err != nil {
				logger.Error("telemetry cycle failed", "error", err)
			}
		}
	}
}

func positiveIntEnvironment(name string, fallback, minimum, maximum int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return parsed, nil
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
