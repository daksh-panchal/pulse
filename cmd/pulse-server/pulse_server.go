package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
