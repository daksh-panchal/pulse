package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/daksh-panchal/pulse/internal/alerting"
	"github.com/daksh-panchal/pulse/internal/httpapi"
	"github.com/daksh-panchal/pulse/internal/storage/postgres"
)

const defaultDatabaseURL = "postgres://pulse:pulse_dev@127.0.0.1:5433/pulse?sslmode=disable"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	address := environment("PULSE_SERVER_ADDRESS", "127.0.0.1:8080")
	databaseURL := environment("PULSE_DATABASE_URL", defaultDatabaseURL)

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 15*time.Second)
	store, err := postgres.Open(startupContext, databaseURL)
	cancelStartup()
	if err != nil {
		logger.Error("start database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	policy, evaluationInterval, err := alertConfiguration()
	if err != nil {
		logger.Error("configure alerting", "error", err)
		os.Exit(1)
	}
	alertEngine, err := alerting.New(store, policy)
	if err != nil {
		logger.Error("create alert engine", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              address,
		Handler:           httpapi.New(store, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go evaluateAlerts(shutdownContext, alertEngine, evaluationInterval, logger)
	go func() {
		<-shutdownContext.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown", "error", err)
		}
	}()

	logger.Info("Pulse server listening", "address", address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve HTTP", "error", err)
		os.Exit(1)
	}
}

func evaluateAlerts(ctx context.Context, engine *alerting.Engine, interval time.Duration, logger *slog.Logger) {
	evaluate := func() {
		evaluationContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		summary, err := engine.Evaluate(evaluationContext, time.Now().UTC())
		if err != nil {
			if ctx.Err() == nil {
				logger.Error("alert evaluation failed", "error", err)
			}
			return
		}
		if summary.Created > 0 || summary.Transitioned > 0 {
			logger.Info("alert evaluation completed", "signals", summary.Signals,
				"created", summary.Created, "transitioned", summary.Transitioned)
		}
	}
	evaluate()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			evaluate()
		}
	}
}

func alertConfiguration() (alerting.Policy, time.Duration, error) {
	policy := alerting.DefaultPolicy()
	var err error
	if policy.CPUThresholdPercent, err = floatEnvironment("PULSE_ALERT_CPU_PERCENT", policy.CPUThresholdPercent); err != nil {
		return alerting.Policy{}, 0, err
	}
	if policy.MemoryThresholdPercent, err = floatEnvironment("PULSE_ALERT_MEMORY_PERCENT", policy.MemoryThresholdPercent); err != nil {
		return alerting.Policy{}, 0, err
	}
	if policy.ResourceSustain, err = durationEnvironment("PULSE_ALERT_RESOURCE_SUSTAIN", policy.ResourceSustain); err != nil {
		return alerting.Policy{}, 0, err
	}
	if policy.AvailabilitySustain, err = durationEnvironment("PULSE_ALERT_AVAILABILITY_SUSTAIN", policy.AvailabilitySustain); err != nil {
		return alerting.Policy{}, 0, err
	}
	if policy.RecoveryDuration, err = durationEnvironment("PULSE_ALERT_RECOVERY", policy.RecoveryDuration); err != nil {
		return alerting.Policy{}, 0, err
	}
	if policy.AgentStaleAfter, err = durationEnvironment("PULSE_ALERT_AGENT_STALE_AFTER", policy.AgentStaleAfter); err != nil {
		return alerting.Policy{}, 0, err
	}
	if policy.ContainerStaleAfter, err = durationEnvironment("PULSE_ALERT_CONTAINER_STALE_AFTER", policy.ContainerStaleAfter); err != nil {
		return alerting.Policy{}, 0, err
	}
	evaluationInterval, err := durationEnvironment("PULSE_ALERT_EVALUATION_INTERVAL", 5*time.Second)
	return policy, evaluationInterval, err
}

func durationEnvironment(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
}

func floatEnvironment(name string, fallback float64) (float64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("%s must be a positive number", name)
	}
	return parsed, nil
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
