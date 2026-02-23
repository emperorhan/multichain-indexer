package fetcher

import (
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/circuitbreaker"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator/autotune"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// --- auto-tune signal sink stub ---

// stubAutoTuneSink records calls to RecordRPCResult and RecordDBCommitLatencyMs
// for assertion. It satisfies autotune.AutoTuneSignalSink.
type stubAutoTuneSink struct {
	rpcCalls []stubRPCCall
}

type stubRPCCall struct {
	chain   string
	network string
	isError bool
}

func (s *stubAutoTuneSink) RecordRPCResult(chain, network string, isError bool) {
	s.rpcCalls = append(s.rpcCalls, stubRPCCall{chain: chain, network: network, isError: isError})
}

func (s *stubAutoTuneSink) RecordDBCommitLatencyMs(_, _ string, _ int64) {
	// Not exercised by fetcher; intentional no-op.
}

// Compile-time interface check.
var _ autotune.AutoTuneSignalSink = (*stubAutoTuneSink)(nil)

// --- tests ---

func TestWithAutoTuneSignalSink_AppliesValue(t *testing.T) {
	sink := &stubAutoTuneSink{}
	f := &Fetcher{}
	opt := WithAutoTuneSignalSink(sink)
	opt(f)

	require.NotNil(t, f.autoTuneSignals, "autoTuneSignals field should be set")
	assert.Same(t, sink, f.autoTuneSignals.(*stubAutoTuneSink),
		"autoTuneSignals should point to the exact sink passed in")
}

func TestRecordRPCResult_WithAutoTuneSink(t *testing.T) {
	sink := &stubAutoTuneSink{}
	f := &Fetcher{autoTuneSignals: sink}

	f.recordRPCResult("solana", "devnet", false)
	f.recordRPCResult("solana", "devnet", true)
	f.recordRPCResult("base", "mainnet", false)

	require.Len(t, sink.rpcCalls, 3)
	assert.Equal(t, stubRPCCall{chain: "solana", network: "devnet", isError: false}, sink.rpcCalls[0])
	assert.Equal(t, stubRPCCall{chain: "solana", network: "devnet", isError: true}, sink.rpcCalls[1])
	assert.Equal(t, stubRPCCall{chain: "base", network: "mainnet", isError: false}, sink.rpcCalls[2])
}

func TestRecordRPCResult_WithCircuitBreaker(t *testing.T) {
	cb := circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: 3,
		SuccessThreshold: 2,
		OpenTimeout:      10 * time.Second,
	})

	f := &Fetcher{circuitBreaker: cb}

	// Record two successes — breaker stays closed.
	f.recordRPCResult("eth", "mainnet", false)
	f.recordRPCResult("eth", "mainnet", false)
	assert.Equal(t, circuitbreaker.StateClosed, cb.GetState())

	// Record three failures — breaker should open (threshold = 3).
	f.recordRPCResult("eth", "mainnet", true)
	f.recordRPCResult("eth", "mainnet", true)
	f.recordRPCResult("eth", "mainnet", true)
	assert.Equal(t, circuitbreaker.StateOpen, cb.GetState())
}

func TestRecordRPCResult_WithBothSinkAndCB(t *testing.T) {
	sink := &stubAutoTuneSink{}
	cb := circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		OpenTimeout:      10 * time.Second,
	})

	f := &Fetcher{
		autoTuneSignals: sink,
		circuitBreaker:  cb,
	}

	// One success: both paths should be exercised.
	f.recordRPCResult("polygon", "mainnet", false)
	require.Len(t, sink.rpcCalls, 1)
	assert.False(t, sink.rpcCalls[0].isError)
	assert.Equal(t, circuitbreaker.StateClosed, cb.GetState())

	// Two errors: sink records both; CB opens after the second.
	f.recordRPCResult("polygon", "mainnet", true)
	f.recordRPCResult("polygon", "mainnet", true)
	require.Len(t, sink.rpcCalls, 3)
	assert.True(t, sink.rpcCalls[1].isError)
	assert.True(t, sink.rpcCalls[2].isError)
	assert.Equal(t, circuitbreaker.StateOpen, cb.GetState(),
		"circuit breaker should open after 2 failures (threshold=2)")
}

func TestRecordRPCResult_NilSinkAndCB(t *testing.T) {
	f := &Fetcher{} // both autoTuneSignals and circuitBreaker are nil

	// Must not panic.
	assert.NotPanics(t, func() {
		f.recordRPCResult("btc", "mainnet", false)
		f.recordRPCResult("btc", "mainnet", true)
	})
}
