package fetcher

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/cache"
	"github.com/emperorhan/multichain-indexer/internal/circuitbreaker"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
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

// ---------------------------------------------------------------------------
// sleep() context cancellation tests
// ---------------------------------------------------------------------------

func TestSleep_ZeroDelay_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &Fetcher{}
	err := f.sleep(context.Background(), 0)
	assert.NoError(t, err)
}

func TestSleep_NegativeDelay_ReturnsNil(t *testing.T) {
	t.Parallel()
	f := &Fetcher{}
	err := f.sleep(context.Background(), -1*time.Second)
	assert.NoError(t, err)
}

func TestSleep_ContextCancel_ReturnsError(t *testing.T) {
	t.Parallel()
	f := &Fetcher{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	err := f.sleep(ctx, 10*time.Second)
	assert.ErrorIs(t, err, context.Canceled)
}

// ---------------------------------------------------------------------------
// New() constructor tests
// ---------------------------------------------------------------------------

func TestNew_DefaultValues(t *testing.T) {
	t.Parallel()
	jobCh := make(chan event.FetchJob)
	rawBatchCh := make(chan event.RawBatch)

	f := New(nil, jobCh, rawBatchCh, 4, slog.Default())

	assert.Equal(t, 4, f.workerCount)
	assert.Equal(t, defaultRetryMaxAttempts, f.retryMaxAttempts)
	assert.Equal(t, defaultBackoffInitial, f.backoffInitial)
	assert.Equal(t, defaultBackoffMax, f.backoffMax)
	assert.Equal(t, defaultAdaptiveMinBatch, f.adaptiveMinBatch)
	assert.NotNil(t, f.batchSizeByAddress)
	assert.NotNil(t, f.logger)
}

func TestNew_WorkerCountClampedToOne(t *testing.T) {
	t.Parallel()
	jobCh := make(chan event.FetchJob)
	rawBatchCh := make(chan event.RawBatch)

	f := New(nil, jobCh, rawBatchCh, 0, slog.Default())
	assert.Equal(t, 1, f.workerCount, "workerCount=0 should be clamped to 1")

	f2 := New(nil, jobCh, rawBatchCh, -5, slog.Default())
	assert.Equal(t, 1, f2.workerCount, "negative workerCount should be clamped to 1")
}

func TestNew_NilLoggerUsesDefault(t *testing.T) {
	t.Parallel()
	jobCh := make(chan event.FetchJob)
	rawBatchCh := make(chan event.RawBatch)

	f := New(nil, jobCh, rawBatchCh, 1, nil)
	assert.NotNil(t, f.logger, "nil logger should fall back to slog.Default()")
}

func TestNew_OptionsApplied(t *testing.T) {
	t.Parallel()
	jobCh := make(chan event.FetchJob)
	rawBatchCh := make(chan event.RawBatch)

	f := New(nil, jobCh, rawBatchCh, 2, slog.Default(),
		WithAdaptiveMinBatch(50),
		WithBlockScanMaxBatchTxs(1000),
	)

	assert.Equal(t, 50, f.adaptiveMinBatch)
	assert.Equal(t, 1000, f.blockScanMaxBatchTxs)
}

func TestNew_NilOptionSkipped(t *testing.T) {
	t.Parallel()
	jobCh := make(chan event.FetchJob)
	rawBatchCh := make(chan event.RawBatch)

	// Should not panic when a nil option is in the slice.
	assert.NotPanics(t, func() {
		_ = New(nil, jobCh, rawBatchCh, 1, slog.Default(), nil, WithAdaptiveMinBatch(10))
	})
}

// ---------------------------------------------------------------------------
// resolveBatchSize tests
// ---------------------------------------------------------------------------

// newTestFetcher creates a minimal Fetcher with a real LRU cache for
// resolveBatchSize / setAdaptiveBatchSize testing.
func newTestFetcher(adaptiveMin int) *Fetcher {
	return &Fetcher{
		adaptiveMinBatch:   adaptiveMin,
		batchSizeByAddress: cache.NewLRU[string, int](1000, time.Hour),
	}
}

func TestResolveBatchSize_NotInCache_ReturnsHardCap(t *testing.T) {
	t.Parallel()
	f := newTestFetcher(1)
	got := f.resolveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", 100)
	assert.Equal(t, 100, got, "first call for an address should return hardCap")

	// Verify it was also stored in the cache.
	key := f.batchStateKey(model.ChainSolana, model.NetworkDevnet, "addr1")
	cached, ok := f.batchSizeByAddress.Get(key)
	assert.True(t, ok)
	assert.Equal(t, 100, cached)
}

func TestResolveBatchSize_HardCapZero_ClampsToOne(t *testing.T) {
	t.Parallel()
	f := newTestFetcher(1)
	got := f.resolveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", 0)
	assert.Equal(t, 1, got, "hardCap=0 should be clamped to 1")
}

func TestResolveBatchSize_HardCapNegative_ClampsToOne(t *testing.T) {
	t.Parallel()
	f := newTestFetcher(1)
	got := f.resolveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", -10)
	assert.Equal(t, 1, got, "negative hardCap should be clamped to 1")
}

func TestResolveBatchSize_CachedSizeExceedsHardCap_ClampsDown(t *testing.T) {
	t.Parallel()
	f := newTestFetcher(1)

	// Seed the cache with a value larger than the hardCap we will pass.
	f.setAdaptiveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", 500)

	got := f.resolveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", 200)
	assert.Equal(t, 200, got, "cached size > hardCap should be clamped down to hardCap")

	// Cache should now hold the clamped value.
	key := f.batchStateKey(model.ChainSolana, model.NetworkDevnet, "addr1")
	cached, _ := f.batchSizeByAddress.Get(key)
	assert.Equal(t, 200, cached)
}

func TestResolveBatchSize_CachedSizeBelowAdaptiveMin_ClampsUp(t *testing.T) {
	t.Parallel()
	f := newTestFetcher(10) // adaptiveMinBatch = 10

	// Seed cache with a value below the adaptive minimum.
	key := f.batchStateKey(model.ChainSolana, model.NetworkDevnet, "addr1")
	f.batchSizeByAddress.Put(key, 3)

	got := f.resolveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", 100)
	assert.Equal(t, 10, got, "cached size < adaptiveMinBatch should be clamped up")

	// Cache should now hold the clamped-up value.
	cached, _ := f.batchSizeByAddress.Get(key)
	assert.Equal(t, 10, cached)
}

func TestResolveBatchSize_CachedSizeInRange_ReturnsAsIs(t *testing.T) {
	t.Parallel()
	f := newTestFetcher(5) // adaptiveMinBatch = 5

	// Seed cache with a value between min and hardCap.
	key := f.batchStateKey(model.ChainSolana, model.NetworkDevnet, "addr1")
	f.batchSizeByAddress.Put(key, 50)

	got := f.resolveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", 100)
	assert.Equal(t, 50, got, "cached size in [adaptiveMin, hardCap] should be returned unchanged")

	// Cache value should remain unchanged.
	cached, _ := f.batchSizeByAddress.Get(key)
	assert.Equal(t, 50, cached)
}

// ---------------------------------------------------------------------------
// setAdaptiveBatchSize tests
// ---------------------------------------------------------------------------

func TestSetAdaptiveBatchSize_StoresValue(t *testing.T) {
	t.Parallel()
	f := newTestFetcher(1)

	f.setAdaptiveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", 42)

	// Verify stored value via resolveBatchSize (with a hardCap larger than 42).
	got := f.resolveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", 100)
	assert.Equal(t, 42, got, "setAdaptiveBatchSize should store the value retrievable by resolveBatchSize")
}

func TestSetAdaptiveBatchSize_ZeroIgnored(t *testing.T) {
	t.Parallel()
	f := newTestFetcher(1)

	f.setAdaptiveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", 0)

	// Since size <= 0 is rejected by setAdaptiveBatchSize, the cache has no entry.
	// resolveBatchSize should fall back to hardCap for the first call.
	got := f.resolveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", 50)
	assert.Equal(t, 50, got, "setAdaptiveBatchSize(0) should not store; resolve should return hardCap")
}

func TestSetAdaptiveBatchSize_NegativeIgnored(t *testing.T) {
	t.Parallel()
	f := newTestFetcher(1)

	f.setAdaptiveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", -5)

	key := f.batchStateKey(model.ChainSolana, model.NetworkDevnet, "addr1")
	_, ok := f.batchSizeByAddress.Get(key)
	assert.False(t, ok, "negative size should not be stored")
}

func TestSetAdaptiveBatchSize_BelowMin_ClampsUp(t *testing.T) {
	t.Parallel()
	f := newTestFetcher(10) // adaptiveMinBatch = 10

	f.setAdaptiveBatchSize(model.ChainSolana, model.NetworkDevnet, "addr1", 3)

	// Should have been clamped up to the adaptive minimum.
	key := f.batchStateKey(model.ChainSolana, model.NetworkDevnet, "addr1")
	cached, ok := f.batchSizeByAddress.Get(key)
	assert.True(t, ok)
	assert.Equal(t, 10, cached, "size below adaptiveMinBatch should be clamped up to min")
}
