package finalizer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chainpkg "github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReorgAwareAdapter struct {
	chainName      string
	finalizedBlock int64
	finalizedErr   error
}

var _ chainpkg.ReorgAwareAdapter = (*fakeReorgAwareAdapter)(nil)

func (f *fakeReorgAwareAdapter) Chain() string { return f.chainName }
func (f *fakeReorgAwareAdapter) GetHeadSequence(context.Context) (int64, error) {
	return 0, nil
}
func (f *fakeReorgAwareAdapter) FetchNewSignatures(_ context.Context, _ string, _ *string, _ int) ([]chainpkg.SignatureInfo, error) {
	return nil, nil
}
func (f *fakeReorgAwareAdapter) FetchTransactions(_ context.Context, _ []string) ([]json.RawMessage, error) {
	return nil, nil
}
func (f *fakeReorgAwareAdapter) GetBlockHash(_ context.Context, _ int64) (string, string, error) {
	return "", "", nil
}
func (f *fakeReorgAwareAdapter) GetFinalizedBlockNumber(context.Context) (int64, error) {
	if f.finalizedErr != nil {
		return 0, f.finalizedErr
	}
	return f.finalizedBlock, nil
}

type fakeBlockRepo struct {
	purgedBefore int64
	purgeCount   int64
	purgeErr     error
}

func (f *fakeBlockRepo) UpsertTx(_ context.Context, _ *sql.Tx, _ *model.IndexedBlock) error {
	return nil
}
func (f *fakeBlockRepo) BulkUpsertTx(_ context.Context, _ *sql.Tx, _ []*model.IndexedBlock) error {
	return nil
}
func (f *fakeBlockRepo) GetUnfinalized(_ context.Context, _ model.Chain, _ model.Network) ([]model.IndexedBlock, error) {
	return nil, nil
}
func (f *fakeBlockRepo) GetByBlockNumber(_ context.Context, _ model.Chain, _ model.Network, _ int64) (*model.IndexedBlock, error) {
	return nil, nil
}
func (f *fakeBlockRepo) UpdateFinalityTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ int64, _ string) error {
	return nil
}
func (f *fakeBlockRepo) DeleteFromBlockTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ int64) (int64, error) {
	return 0, nil
}
func (f *fakeBlockRepo) PurgeFinalizedBefore(_ context.Context, _ model.Chain, _ model.Network, beforeBlock int64) (int64, error) {
	f.purgedBefore = beforeBlock
	if f.purgeErr != nil {
		return 0, f.purgeErr
	}
	return f.purgeCount, nil
}

func TestFinalizer_SendsPromotionWhenBlockAdvances(t *testing.T) {
	t.Parallel()

	adapter := &fakeReorgAwareAdapter{
		chainName:      "base",
		finalizedBlock: 100,
	}
	finalityCh := make(chan event.FinalityPromotion, 1)

	f := New(model.ChainBase, model.NetworkSepolia, adapter, &fakeBlockRepo{}, finalityCh, time.Second, slog.Default())

	err := f.check(context.Background())
	require.NoError(t, err)

	select {
	case promo := <-finalityCh:
		assert.Equal(t, model.ChainBase, promo.Chain)
		assert.Equal(t, model.NetworkSepolia, promo.Network)
		assert.Equal(t, int64(100), promo.NewFinalizedBlock)
	default:
		t.Fatal("expected finality promotion")
	}

	// Verify lastFinalized was updated
	assert.Equal(t, int64(100), f.lastFinalized)
}

func TestFinalizer_SkipsWhenNoAdvance(t *testing.T) {
	t.Parallel()

	adapter := &fakeReorgAwareAdapter{
		chainName:      "base",
		finalizedBlock: 100,
	}
	finalityCh := make(chan event.FinalityPromotion, 1)

	f := New(model.ChainBase, model.NetworkSepolia, adapter, &fakeBlockRepo{}, finalityCh, time.Second, slog.Default())
	f.lastFinalized = 100 // Already at 100

	err := f.check(context.Background())
	require.NoError(t, err)

	select {
	case <-finalityCh:
		t.Fatal("should not have sent promotion when no advance")
	default:
	}
}

func TestFinalizer_AdvancesOnNewFinalized(t *testing.T) {
	t.Parallel()

	adapter := &fakeReorgAwareAdapter{
		chainName:      "ethereum",
		finalizedBlock: 200,
	}
	finalityCh := make(chan event.FinalityPromotion, 1)

	f := New(model.ChainEthereum, model.NetworkMainnet, adapter, &fakeBlockRepo{}, finalityCh, time.Second, slog.Default())
	f.lastFinalized = 150 // Previously at 150

	err := f.check(context.Background())
	require.NoError(t, err)

	select {
	case promo := <-finalityCh:
		assert.Equal(t, int64(200), promo.NewFinalizedBlock)
	default:
		t.Fatal("expected finality promotion")
	}

	assert.Equal(t, int64(200), f.lastFinalized)
}

func TestFinalizer_PrunesOldFinalizedBlocks(t *testing.T) {
	t.Parallel()

	adapter := &fakeReorgAwareAdapter{
		chainName:      "base",
		finalizedBlock: 20000,
	}
	repo := &fakeBlockRepo{purgeCount: 5000}
	finalityCh := make(chan event.FinalityPromotion, 1)

	f := New(model.ChainBase, model.NetworkSepolia, adapter, repo, finalityCh, time.Second, slog.Default(),
		WithRetentionBlocks(10000),
	)

	err := f.check(context.Background())
	require.NoError(t, err)

	// Drain the promotion
	<-finalityCh

	// Verify pruning was called with correct cutoff: 20000 - 10000 = 10000
	assert.Equal(t, int64(10000), repo.purgedBefore)
}

func TestFinalizer_NoPruneWhenRetentionZero(t *testing.T) {
	t.Parallel()

	adapter := &fakeReorgAwareAdapter{
		chainName:      "base",
		finalizedBlock: 20000,
	}
	repo := &fakeBlockRepo{}
	finalityCh := make(chan event.FinalityPromotion, 1)

	// retention=0 (default) means no pruning
	f := New(model.ChainBase, model.NetworkSepolia, adapter, repo, finalityCh, time.Second, slog.Default())

	err := f.check(context.Background())
	require.NoError(t, err)

	<-finalityCh

	// purgedBefore should remain zero (PurgeFinalizedBefore was never called)
	assert.Equal(t, int64(0), repo.purgedBefore)
}

func TestFinalizer_NoPruneWhenCutoffNotPositive(t *testing.T) {
	t.Parallel()

	adapter := &fakeReorgAwareAdapter{
		chainName:      "base",
		finalizedBlock: 5000,
	}
	repo := &fakeBlockRepo{}
	finalityCh := make(chan event.FinalityPromotion, 1)

	// retention is larger than finalized block number, cutoff would be negative
	f := New(model.ChainBase, model.NetworkSepolia, adapter, repo, finalityCh, time.Second, slog.Default(),
		WithRetentionBlocks(10000),
	)

	err := f.check(context.Background())
	require.NoError(t, err)

	<-finalityCh

	// purgedBefore should remain zero (cutoff <= 0 so no pruning)
	assert.Equal(t, int64(0), repo.purgedBefore)
}

// ---------------------------------------------------------------------------
// Thread-safe adapter for Run() tests
// ---------------------------------------------------------------------------

// concurrentFakeAdapter is a thread-safe fake that tracks call counts and
// allows dynamic control of the returned finalized block and error.
type concurrentFakeAdapter struct {
	mu             sync.Mutex
	chainName      string
	finalizedBlock int64
	finalizedErr   error
	callCount      atomic.Int64
}

var _ chainpkg.ReorgAwareAdapter = (*concurrentFakeAdapter)(nil)

func (a *concurrentFakeAdapter) Chain() string { return a.chainName }
func (a *concurrentFakeAdapter) GetHeadSequence(context.Context) (int64, error) {
	return 0, nil
}
func (a *concurrentFakeAdapter) FetchNewSignatures(_ context.Context, _ string, _ *string, _ int) ([]chainpkg.SignatureInfo, error) {
	return nil, nil
}
func (a *concurrentFakeAdapter) FetchTransactions(_ context.Context, _ []string) ([]json.RawMessage, error) {
	return nil, nil
}
func (a *concurrentFakeAdapter) GetBlockHash(_ context.Context, _ int64) (string, string, error) {
	return "", "", nil
}
func (a *concurrentFakeAdapter) GetFinalizedBlockNumber(context.Context) (int64, error) {
	a.callCount.Add(1)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finalizedErr != nil {
		return 0, a.finalizedErr
	}
	return a.finalizedBlock, nil
}

func (a *concurrentFakeAdapter) setBlock(block int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finalizedBlock = block
}

func (a *concurrentFakeAdapter) setError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finalizedErr = err
}

// ---------------------------------------------------------------------------
// Run() loop tests
// ---------------------------------------------------------------------------

func TestFinalizer_Run_ContextCancellation(t *testing.T) {
	t.Parallel()

	adapter := &concurrentFakeAdapter{
		chainName:      "base",
		finalizedBlock: 100,
	}
	finalityCh := make(chan event.FinalityPromotion, 10)

	f := New(model.ChainBase, model.NetworkSepolia, adapter, &fakeBlockRepo{}, finalityCh, 50*time.Millisecond, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- f.Run(ctx)
	}()

	// Cancel the context promptly
	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return promptly after context cancellation")
	}
}

func TestFinalizer_Run_TickerFiresAndCallsCheck(t *testing.T) {
	t.Parallel()

	adapter := &concurrentFakeAdapter{
		chainName:      "base",
		finalizedBlock: 100,
	}
	finalityCh := make(chan event.FinalityPromotion, 100)

	f := New(model.ChainBase, model.NetworkSepolia, adapter, &fakeBlockRepo{}, finalityCh, 10*time.Millisecond, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- f.Run(ctx)
	}()

	// Wait long enough for at least one tick to fire (give generous headroom)
	require.Eventually(t, func() bool {
		return adapter.callCount.Load() >= 1
	}, 2*time.Second, 5*time.Millisecond, "expected GetFinalizedBlockNumber to be called at least once")

	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}

func TestFinalizer_Run_SendsPromotionOnChannel(t *testing.T) {
	t.Parallel()

	adapter := &concurrentFakeAdapter{
		chainName:      "base",
		finalizedBlock: 500,
	}
	finalityCh := make(chan event.FinalityPromotion, 10)

	f := New(model.ChainBase, model.NetworkSepolia, adapter, &fakeBlockRepo{}, finalityCh, 10*time.Millisecond, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- f.Run(ctx)
	}()

	// Read the first promotion from the channel
	select {
	case promo := <-finalityCh:
		assert.Equal(t, model.ChainBase, promo.Chain)
		assert.Equal(t, model.NetworkSepolia, promo.Network)
		assert.Equal(t, int64(500), promo.NewFinalizedBlock)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for FinalityPromotion from Run()")
	}

	// Advance the finalized block and verify a second promotion is sent
	adapter.setBlock(600)

	select {
	case promo := <-finalityCh:
		assert.Equal(t, int64(600), promo.NewFinalizedBlock)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second FinalityPromotion")
	}

	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}

func TestFinalizer_Run_CheckErrorDoesNotCrash(t *testing.T) {
	t.Parallel()

	adapter := &concurrentFakeAdapter{
		chainName:    "base",
		finalizedErr: errors.New("rpc unavailable"),
	}
	finalityCh := make(chan event.FinalityPromotion, 10)

	f := New(model.ChainBase, model.NetworkSepolia, adapter, &fakeBlockRepo{}, finalityCh, 10*time.Millisecond, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- f.Run(ctx)
	}()

	// Wait for a few ticks to confirm Run() keeps running despite errors
	require.Eventually(t, func() bool {
		return adapter.callCount.Load() >= 3
	}, 2*time.Second, 5*time.Millisecond, "expected at least 3 calls despite errors")

	// No promotion should have been sent since every check fails
	select {
	case promo := <-finalityCh:
		t.Fatalf("unexpected promotion received: %+v", promo)
	default:
		// Good: no promotion was sent
	}

	// Now clear the error and set a valid block; Run() should recover
	adapter.setError(nil)
	adapter.setBlock(999)

	select {
	case promo := <-finalityCh:
		assert.Equal(t, int64(999), promo.NewFinalizedBlock)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for promotion after error recovery")
	}

	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}
