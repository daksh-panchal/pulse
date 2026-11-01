package alerting

import "time"

type Kind string

const (
	KindHighCPU              Kind = "high_cpu"
	KindHighMemory           Kind = "high_memory"
	KindContainerUnavailable Kind = "container_unavailable"
	KindAgentStale           Kind = "agent_stale"
)

type State string

const (
	StatePending    State = "pending"
	StateFiring     State = "firing"
	StateRecovering State = "recovering"
	StateResolved   State = "resolved"
)

type Policy struct {
	CPUThresholdPercent    float64
	MemoryThresholdPercent float64
	ResourceSustain        time.Duration
	AvailabilitySustain    time.Duration
	RecoveryDuration       time.Duration
	AgentStaleAfter        time.Duration
	ContainerStaleAfter    time.Duration
}

func DefaultPolicy() Policy {
	return Policy{
		CPUThresholdPercent:    80,
		MemoryThresholdPercent: 85,
		ResourceSustain:        30 * time.Second,
		AvailabilitySustain:    10 * time.Second,
		RecoveryDuration:       15 * time.Second,
		AgentStaleAfter:        30 * time.Second,
		ContainerStaleAfter:    20 * time.Second,
	}
}

func (policy Policy) SustainFor(kind Kind) time.Duration {
	if kind == KindHighCPU || kind == KindHighMemory {
		return policy.ResourceSustain
	}
	return policy.AvailabilitySustain
}

type Signal struct {
	Fingerprint   string
	Kind          Kind
	AgentID       string
	ContainerID   string
	Title         string
	Known         bool
	Breached      bool
	Threshold     *float64
	ObservedValue *float64
	ObservedAt    time.Time
	Details       map[string]any
}

type Incident struct {
	ID                int64          `json:"id"`
	Fingerprint       string         `json:"fingerprint"`
	Kind              Kind           `json:"kind"`
	AgentID           string         `json:"agent_id"`
	ContainerID       string         `json:"container_id,omitempty"`
	State             State          `json:"state"`
	Title             string         `json:"title"`
	Threshold         *float64       `json:"threshold,omitempty"`
	ObservedValue     *float64       `json:"observed_value,omitempty"`
	OpenedAt          time.Time      `json:"opened_at"`
	FiringAt          *time.Time     `json:"firing_at,omitempty"`
	RecoveryStartedAt *time.Time     `json:"recovery_started_at,omitempty"`
	ResolvedAt        *time.Time     `json:"resolved_at,omitempty"`
	LastEvaluatedAt   time.Time      `json:"last_evaluated_at"`
	LastObservedAt    time.Time      `json:"last_observed_at"`
	Details           map[string]any `json:"details,omitempty"`
}

type View string

const (
	ViewActive  View = "active"
	ViewHistory View = "history"
	ViewAll     View = "all"
)

type EvaluationSummary struct {
	Signals      int
	Created      int
	Transitioned int
}
