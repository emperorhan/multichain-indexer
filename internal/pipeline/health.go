package pipeline

import (
	"sync"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// HealthStatus represents the health state of a pipeline.
type HealthStatus string

const (
	HealthStatusUnknown   HealthStatus = "UNKNOWN"
	HealthStatusHealthy   HealthStatus = "HEALTHY"
	HealthStatusUnhealthy HealthStatus = "UNHEALTHY"
	HealthStatusInactive  HealthStatus = "INACTIVE"

	// DefaultUnhealthyThreshold is the number of consecutive failures
	// before a pipeline is considered unhealthy.
	DefaultUnhealthyThreshold = 5
)

// PipelineHealth tracks the health state of a single pipeline instance.
type PipelineHealth struct {
	mu                  sync.RWMutex
	chain               model.Chain
	network             model.Network
	status              HealthStatus
	consecutiveFailures int
	lastSuccessAt       *time.Time
	lastFailureAt       *time.Time
	unhealthyThreshold  int
}

// NewPipelineHealth creates a new health tracker for the given chain/network.
func NewPipelineHealth(chain model.Chain, network model.Network) *PipelineHealth {
	return &PipelineHealth{
		chain:              chain,
		network:            network,
		status:             HealthStatusUnknown,
		unhealthyThreshold: DefaultUnhealthyThreshold,
	}
}

// SetStatus sets the health status directly.
func (h *PipelineHealth) SetStatus(status HealthStatus) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status = status
}

// RecordSuccess records a successful pipeline tick/cycle.
func (h *PipelineHealth) RecordSuccess() {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	wasUnhealthy := h.status == HealthStatusUnhealthy
	h.consecutiveFailures = 0
	h.lastSuccessAt = &now
	h.status = HealthStatusHealthy
	_ = wasUnhealthy // used by callers to detect recovery
}

// RecordSuccessWithRecovery records a success and returns true if it
// represents a recovery from an unhealthy state.
func (h *PipelineHealth) RecordSuccessWithRecovery() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	wasUnhealthy := h.status == HealthStatusUnhealthy
	h.consecutiveFailures = 0
	h.lastSuccessAt = &now
	h.status = HealthStatusHealthy
	return wasUnhealthy
}

// RecordFailure records a pipeline failure. Returns true if the pipeline
// transitioned to unhealthy on this call.
func (h *PipelineHealth) RecordFailure() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	h.consecutiveFailures++
	h.lastFailureAt = &now
	if h.consecutiveFailures >= h.unhealthyThreshold && h.status != HealthStatusUnhealthy {
		h.status = HealthStatusUnhealthy
		return true
	}
	return false
}

// Snapshot returns the current health state.
func (h *PipelineHealth) Snapshot() HealthSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return HealthSnapshot{
		Chain:               string(h.chain),
		Network:             string(h.network),
		Status:              string(h.status),
		ConsecutiveFailures: h.consecutiveFailures,
		LastSuccessAt:       h.lastSuccessAt,
		LastFailureAt:       h.lastFailureAt,
	}
}

// HealthSnapshot is a point-in-time view of pipeline health (JSON-safe).
type HealthSnapshot struct {
	Chain               string     `json:"chain"`
	Network             string     `json:"network"`
	Status              string     `json:"status"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt       *time.Time `json:"last_failure_at,omitempty"`
}
