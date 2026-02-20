package pipeline

import (
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

func TestPipelineHealth_RecordSuccess(t *testing.T) {
	h := NewPipelineHealth(model.ChainSolana, "devnet")
	h.RecordSuccess()

	snap := h.Snapshot()
	assert.Equal(t, string(HealthStatusHealthy), snap.Status)
	assert.Equal(t, 0, snap.ConsecutiveFailures)
	assert.NotNil(t, snap.LastSuccessAt)
}

func TestPipelineHealth_RecordFailure_Threshold(t *testing.T) {
	h := NewPipelineHealth(model.ChainSolana, "devnet")
	for i := 0; i < DefaultUnhealthyThreshold-1; i++ {
		transitioned := h.RecordFailure()
		assert.False(t, transitioned, "should not transition before threshold")
	}

	transitioned := h.RecordFailure()
	assert.True(t, transitioned, "should transition at threshold")
	assert.Equal(t, string(HealthStatusUnhealthy), h.Snapshot().Status)
}

func TestPipelineHealth_RecordSuccessWithRecovery(t *testing.T) {
	h := NewPipelineHealth(model.ChainSolana, "devnet")
	// Make it unhealthy
	for i := 0; i < DefaultUnhealthyThreshold; i++ {
		h.RecordFailure()
	}
	assert.Equal(t, string(HealthStatusUnhealthy), h.Snapshot().Status)

	recovered := h.RecordSuccessWithRecovery()
	assert.True(t, recovered)
	assert.Equal(t, string(HealthStatusHealthy), h.Snapshot().Status)
}

func TestPipelineHealth_RecordLatency_Degraded(t *testing.T) {
	h := NewPipelineHealth(model.ChainBase, "sepolia")
	h.RecordSuccess() // set HEALTHY first

	// Record latencies above threshold
	for i := 0; i < latencyWindowSize; i++ {
		h.RecordLatency(10 * time.Second)
	}

	assert.Equal(t, string(HealthStatusDegraded), h.Snapshot().Status)
}

func TestPipelineHealth_RecordLatency_RecoverFromDegraded(t *testing.T) {
	h := NewPipelineHealth(model.ChainBase, "sepolia")
	h.RecordSuccess()

	// Fill window with high latencies to trigger DEGRADED
	for i := 0; i < latencyWindowSize; i++ {
		h.RecordLatency(10 * time.Second)
	}
	assert.Equal(t, string(HealthStatusDegraded), h.Snapshot().Status)

	// Replace with low latencies
	for i := 0; i < latencyWindowSize; i++ {
		h.RecordLatency(100 * time.Millisecond)
	}

	// Should recover when success is recorded
	h.RecordSuccess()
	assert.Equal(t, string(HealthStatusHealthy), h.Snapshot().Status)
}

func TestPipelineHealth_RecordLatency_DoesNotOverrideUnhealthy(t *testing.T) {
	h := NewPipelineHealth(model.ChainBase, "sepolia")
	// Make unhealthy
	for i := 0; i < DefaultUnhealthyThreshold; i++ {
		h.RecordFailure()
	}
	assert.Equal(t, string(HealthStatusUnhealthy), h.Snapshot().Status)

	// Recording low latency should NOT change unhealthy to degraded or healthy
	h.RecordLatency(10 * time.Millisecond)
	assert.Equal(t, string(HealthStatusUnhealthy), h.Snapshot().Status)
}

func TestPipelineHealth_SetStatus(t *testing.T) {
	h := NewPipelineHealth(model.ChainSolana, "devnet")
	h.SetStatus(HealthStatusInactive)
	assert.Equal(t, string(HealthStatusInactive), h.Snapshot().Status)
}

func TestPipelineHealth_Snapshot_Fields(t *testing.T) {
	h := NewPipelineHealth(model.ChainSolana, "devnet")
	snap := h.Snapshot()

	assert.Equal(t, "solana", snap.Chain)
	assert.Equal(t, "devnet", snap.Network)
	assert.Equal(t, string(HealthStatusUnknown), snap.Status)
	assert.Nil(t, snap.LastSuccessAt)
	assert.Nil(t, snap.LastFailureAt)
}

func TestPipelineHealth_RecordSuccessAfterHighLatency_Degraded(t *testing.T) {
	h := NewPipelineHealth(model.ChainBase, "sepolia")

	// Fill latency window with high values
	for i := 0; i < latencyWindowSize; i++ {
		h.RecordLatency(10 * time.Second)
	}

	// Record success — should set DEGRADED not HEALTHY
	h.RecordSuccess()
	assert.Equal(t, string(HealthStatusDegraded), h.Snapshot().Status)
}
