package alerting

import (
	"context"
	"fmt"
	"math"
	"time"
)

type Repository interface {
	ListAlertSignals(context.Context, time.Time, Policy) ([]Signal, error)
	FindActiveIncident(context.Context, string) (*Incident, error)
	CreateIncident(context.Context, Incident) (Incident, error)
	UpdateIncident(context.Context, Incident) error
}

type Engine struct {
	repository Repository
	policy     Policy
}

func New(repository Repository, policy Policy) (*Engine, error) {
	if err := validatePolicy(policy); err != nil {
		return nil, err
	}
	return &Engine{repository: repository, policy: policy}, nil
}

func (engine *Engine) Evaluate(ctx context.Context, now time.Time) (EvaluationSummary, error) {
	now = now.UTC()
	signals, err := engine.repository.ListAlertSignals(ctx, now, engine.policy)
	if err != nil {
		return EvaluationSummary{}, fmt.Errorf("list alert signals: %w", err)
	}
	summary := EvaluationSummary{Signals: len(signals)}
	for _, signal := range signals {
		active, err := engine.repository.FindActiveIncident(ctx, signal.Fingerprint)
		if err != nil {
			return summary, fmt.Errorf("find active incident %s: %w", signal.Fingerprint, err)
		}
		next, changed := transition(active, signal, now, engine.policy)
		if next == nil {
			continue
		}
		if active == nil {
			created, err := engine.repository.CreateIncident(ctx, *next)
			if err != nil {
				return summary, fmt.Errorf("create incident %s: %w", signal.Fingerprint, err)
			}
			_ = created
			summary.Created++
			continue
		}
		if changed {
			stateChanged := active.State != next.State
			if err := engine.repository.UpdateIncident(ctx, *next); err != nil {
				return summary, fmt.Errorf("update incident %d: %w", next.ID, err)
			}
			if stateChanged {
				summary.Transitioned++
			}
		}
	}
	return summary, nil
}

func transition(active *Incident, signal Signal, now time.Time, policy Policy) (*Incident, bool) {
	if !signal.Known {
		return active, false
	}
	if active == nil {
		if !signal.Breached {
			return nil, false
		}
		return &Incident{
			Fingerprint:     signal.Fingerprint,
			Kind:            signal.Kind,
			AgentID:         signal.AgentID,
			ContainerID:     signal.ContainerID,
			State:           StatePending,
			Title:           signal.Title,
			Threshold:       signal.Threshold,
			ObservedValue:   signal.ObservedValue,
			OpenedAt:        now,
			LastEvaluatedAt: now,
			LastObservedAt:  signal.ObservedAt,
			Details:         signal.Details,
		}, true
	}

	next := *active
	next.LastEvaluatedAt = now
	next.LastObservedAt = signal.ObservedAt
	next.ObservedValue = signal.ObservedValue
	next.Details = signal.Details
	previousState := next.State

	if signal.Breached {
		switch next.State {
		case StatePending:
			if now.Sub(next.OpenedAt) >= policy.SustainFor(next.Kind) {
				next.State = StateFiring
				firingAt := now
				next.FiringAt = &firingAt
			}
		case StateRecovering:
			next.State = StateFiring
			next.RecoveryStartedAt = nil
		}
	} else {
		switch next.State {
		case StatePending:
			next.State = StateResolved
			resolvedAt := now
			next.ResolvedAt = &resolvedAt
		case StateFiring:
			next.State = StateRecovering
			recoveryStartedAt := now
			next.RecoveryStartedAt = &recoveryStartedAt
		case StateRecovering:
			if next.RecoveryStartedAt != nil && now.Sub(*next.RecoveryStartedAt) >= policy.RecoveryDuration {
				next.State = StateResolved
				resolvedAt := now
				next.ResolvedAt = &resolvedAt
			}
		}
	}
	return &next, next.State != previousState || signal.Breached
}

func validatePolicy(policy Policy) error {
	if policy.CPUThresholdPercent <= 0 || policy.MemoryThresholdPercent <= 0 ||
		math.IsNaN(policy.CPUThresholdPercent) || math.IsNaN(policy.MemoryThresholdPercent) ||
		math.IsInf(policy.CPUThresholdPercent, 0) || math.IsInf(policy.MemoryThresholdPercent, 0) {
		return fmt.Errorf("resource thresholds must be positive")
	}
	if policy.ResourceSustain < 0 || policy.AvailabilitySustain < 0 || policy.RecoveryDuration < 0 {
		return fmt.Errorf("alert durations must not be negative")
	}
	if policy.AgentStaleAfter <= 0 || policy.ContainerStaleAfter <= 0 {
		return fmt.Errorf("staleness durations must be positive")
	}
	return nil
}
