package storage

import (
	"context"
	"time"

	"github.com/daksh-panchal/pulse/internal/telemetry"
)

type Store interface {
	Ping(context.Context) error
	Ingest(context.Context, telemetry.Batch) (accepted bool, err error)
	ListCurrentContainers(context.Context) ([]telemetry.StoredSample, error)
	ListMetrics(context.Context, string, time.Time, time.Time, int) ([]telemetry.StoredSample, error)
	Close()
}
