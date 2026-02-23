package fetcher

import (
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/circuitbreaker"
	"github.com/stretchr/testify/assert"
)

func TestWithRetryConfig_AppliesValues(t *testing.T) {
	f := &Fetcher{}
	opt := WithRetryConfig(5, 100*time.Millisecond, 10*time.Second)
	opt(f)
	assert.Equal(t, 5, f.retryMaxAttempts)
	assert.Equal(t, 100*time.Millisecond, f.backoffInitial)
	assert.Equal(t, 10*time.Second, f.backoffMax)
}

func TestWithAdaptiveMinBatch_AppliesValue(t *testing.T) {
	f := &Fetcher{}
	opt := WithAdaptiveMinBatch(10)
	opt(f)
	assert.Equal(t, 10, f.adaptiveMinBatch)
}

func TestWithBoundaryOverlapLookahead_AppliesValue(t *testing.T) {
	f := &Fetcher{}
	opt := WithBoundaryOverlapLookahead(5)
	opt(f)
	assert.Equal(t, 5, f.boundaryOverlapLookahead)
}

func TestWithBlockScanMaxBatchTxs_AppliesValue(t *testing.T) {
	f := &Fetcher{}
	opt := WithBlockScanMaxBatchTxs(1000)
	opt(f)
	assert.Equal(t, 1000, f.blockScanMaxBatchTxs)
}

func TestWithCircuitBreaker_AppliesValues(t *testing.T) {
	called := false
	cb := func(from, to circuitbreaker.State) { called = true }

	f := &Fetcher{}
	opt := WithCircuitBreaker(5, 3, 30*time.Second, cb)
	opt(f)

	assert.Equal(t, 5, f.cbFailureThreshold)
	assert.Equal(t, 3, f.cbSuccessThreshold)
	assert.Equal(t, 30*time.Second, f.cbOpenTimeout)
	assert.NotNil(t, f.cbOnStateChange)

	// Verify the callback is the one we passed.
	f.cbOnStateChange(circuitbreaker.StateClosed, circuitbreaker.StateOpen)
	assert.True(t, called)
}

func TestWithCircuitBreaker_NilCallback(t *testing.T) {
	f := &Fetcher{}
	opt := WithCircuitBreaker(3, 2, 15*time.Second, nil)
	opt(f)

	assert.Equal(t, 3, f.cbFailureThreshold)
	assert.Equal(t, 2, f.cbSuccessThreshold)
	assert.Equal(t, 15*time.Second, f.cbOpenTimeout)
	assert.Nil(t, f.cbOnStateChange)
}
