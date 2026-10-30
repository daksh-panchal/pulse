package telemetry

import "time"

const (
	APIVersion      = 1
	MaxBatchSamples = 500
)

// Batch is the versioned ingestion envelope sent by one Pulse agent.
type Batch struct {
	Version  int       `json:"version"`
	Agent    Agent     `json:"agent"`
	Sequence int64     `json:"sequence"`
	SentAt   time.Time `json:"sent_at"`
	Samples  []Sample  `json:"samples"`
}

type Agent struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
}

// Sample contains one observation of one immutable Docker container instance.
// Pointer metrics distinguish a genuine zero from an unavailable measurement.
type Sample struct {
	ContainerID              string            `json:"container_id"`
	ContainerName            string            `json:"container_name"`
	Image                    string            `json:"image"`
	Platform                 string            `json:"platform,omitempty"`
	CreatedAt                time.Time         `json:"created_at"`
	SampledAt                time.Time         `json:"sampled_at"`
	State                    string            `json:"state"`
	Health                   string            `json:"health"`
	Labels                   map[string]string `json:"labels,omitempty"`
	CPULimitCores            *float64          `json:"cpu_limit_cores,omitempty"`
	CPUUsedCores             *float64          `json:"cpu_used_cores,omitempty"`
	CPUDockerPercent         *float64          `json:"cpu_docker_percent,omitempty"`
	CPUUtilizationPercent    *float64          `json:"cpu_utilization_percent,omitempty"`
	MemoryRawBytes           *uint64           `json:"memory_raw_bytes,omitempty"`
	MemoryWorkingSetBytes    *uint64           `json:"memory_working_set_bytes,omitempty"`
	MemoryLimitBytes         *uint64           `json:"memory_limit_bytes,omitempty"`
	MemoryUtilizationPercent *float64          `json:"memory_utilization_percent,omitempty"`
	NetworkRxBytes           *uint64           `json:"network_rx_bytes,omitempty"`
	NetworkTxBytes           *uint64           `json:"network_tx_bytes,omitempty"`
	CollectionError          string            `json:"collection_error,omitempty"`
}

type StoredSample struct {
	Sample
	AgentID    string    `json:"agent_id"`
	ReceivedAt time.Time `json:"received_at"`
}

type CurrentContainer struct {
	StoredSample
	Freshness string `json:"freshness"`
}
