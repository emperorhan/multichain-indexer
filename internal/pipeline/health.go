package pipeline

import (
	"sort"
	"sync"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// HealthStatus represents the health state of a pipeline.
type HealthStatus string

const (
	HealthStatusUnknown   HealthStatus = "UNKNOWN"
	HealthStatusHealthy   HealthStatus = "HEALTHY"
	HealthStatusDegraded  HealthStatus = "DEGRADED"
	HealthStatusUnhealthy HealthStatus = "UNHEALTHY"
	HealthStatusInactive  HealthStatus = "INACTIVE"

	// DefaultUnhealthyThreshold is the number of consecutive failures
	// before a pipeline is considered unhealthy.
	DefaultUnhealthyThreshold = 5

	// DefaultDegradedLatencyThreshold is the P95 latency threshold
	// before a pipeline is considered degraded.
	DefaultDegradedLatencyThreshold = 5 * time.Second

	// latencyWindowSize is the number of recent latencies tracked.
	latencyWindowSize = 10
)

// PipelineHealth tracks the health state of a single pipeline instance.
type PipelineHealth struct {
	mu                       sync.RWMutex
	chain                    model.Chain
	network                  model.Network
	status                   HealthStatus
	consecutiveFailures      int
	lastSuccessAt            *time.Time
	lastFailureAt            *time.Time
	unhealthyThreshold       int
	latencyBuf               [latencyWindowSize]time.Duration
	latencyHead              int
	latencyCount             int
	degradedLatencyThreshold time.Duration
}

// NewPipelineHealth creates a new health tracker for the given chain/network.
func NewPipelineHealth(chain model.Chain, network model.Network) *PipelineHealth {
	return &PipelineHealth{
		chain:                    chain,
		network:                  network,
		status:                   HealthStatusUnknown,
		unhealthyThreshold:       DefaultUnhealthyThreshold,
		degradedLatencyThreshold: DefaultDegradedLatencyThreshold,
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
	h.consecutiveFailures = 0
	h.lastSuccessAt = &now
	if h.isLatencyDegraded() {
		h.status = HealthStatusDegraded
	} else {
		h.status = HealthStatusHealthy
	}
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
	if h.isLatencyDegraded() {
		h.status = HealthStatusDegraded
	} else {
		h.status = HealthStatusHealthy
	}
	return wasUnhealthy
}

// RecordLatency records a processing latency and updates degraded state.
func (h *PipelineHealth) RecordLatency(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.latencyBuf[h.latencyHead] = d
	h.latencyHead = (h.latencyHead + 1) % latencyWindowSize
	if h.latencyCount < latencyWindowSize {
		h.latencyCount++
	}

	if h.status == HealthStatusHealthy || h.status == HealthStatusDegraded {
		if h.isLatencyDegraded() {
			h.status = HealthStatusDegraded
		} else if h.status == HealthStatusDegraded && h.consecutiveFailures == 0 {
			h.status = HealthStatusHealthy
		}
	}
}

// isLatencyDegraded returns true if the P95 latency exceeds the threshold.
// Must be called with mu held.
func (h *PipelineHealth) isLatencyDegraded() bool {
	if h.latencyCount < 2 {
		return false
	}
	p95 := h.percentileLatency(95)
	return p95 > h.degradedLatencyThreshold
}

// percentileLatency computes the given percentile from recent latencies.
// Must be called with mu held.
func (h *PipelineHealth) percentileLatency(pct int) time.Duration {
	n := h.latencyCount
	if n == 0 {
		return 0
	}
	var sorted [latencyWindowSize]time.Duration
	copy(sorted[:n], h.latencyBuf[:n])
	sort.Slice(sorted[:n], func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := (pct*n - 1) / 100
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx]
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
