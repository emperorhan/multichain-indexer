package reorgdetector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/alert"
	chainpkg "github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingAlerter counts how many alerts were sent.
type countingAlerter struct {
	sent *int
}

func (c *countingAlerter) Send(_ context.Context, _ alert.Alert) error {
	*c.sent++
	return nil
}

// fakeReorgAwareAdapter implements chain.ReorgAwareAdapter for testing.
type fakeReorgAwareAdapter struct {
	chainName      string
	headSequence   int64
	blockHashes    map[int64]string
	parentHashes   map[int64]string
	finalizedBlock int64
	hashErr        error
}

var _ chainpkg.ReorgAwareAdapter = (*fakeReorgAwareAdapter)(nil)

func (f *fakeReorgAwareAdapter) Chain() string { return f.chainName }
func (f *fakeReorgAwareAdapter) GetHeadSequence(context.Context) (int64, error) {
	return f.headSequence, nil
}
func (f *fakeReorgAwareAdapter) FetchNewSignatures(_ context.Context, _ string, _ *string, _ int) ([]chainpkg.SignatureInfo, error) {
	return nil, nil
}
func (f *fakeReorgAwareAdapter) FetchTransactions(_ context.Context, _ []string) ([]json.RawMessage, error) {
	return nil, nil
}

func (f *fakeReorgAwareAdapter) GetBlockHash(_ context.Context, blockNumber int64) (string, string, error) {
	if f.hashErr != nil {
		return "", "", f.hashErr
	}
	hash := f.blockHashes[blockNumber]
	parent := f.parentHashes[blockNumber]
	if hash == "" {
		return "", "", fmt.Errorf("block %d not found", blockNumber)
	}
	return hash, parent, nil
}

func (f *fakeReorgAwareAdapter) GetFinalizedBlockNumber(context.Context) (int64, error) {
	return f.finalizedBlock, nil
}

// fakeBlockRepo implements store.IndexedBlockRepository for testing.
type fakeBlockRepo struct {
	unfinalizedBlocks []model.IndexedBlock
	getErr            error
}

func (f *fakeBlockRepo) UpsertTx(_ context.Context, _ *sql.Tx, _ *model.IndexedBlock) error {
	return nil
}
func (f *fakeBlockRepo) BulkUpsertTx(_ context.Context, _ *sql.Tx, _ []*model.IndexedBlock) error {
	return nil
}
func (f *fakeBlockRepo) GetUnfinalized(_ context.Context, _ model.Chain, _ model.Network) ([]model.IndexedBlock, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.unfinalizedBlocks, nil
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
func (f *fakeBlockRepo) PurgeFinalizedBefore(_ context.Context, _ model.Chain, _ model.Network, _ int64) (int64, error) {
	return 0, nil
}

func TestDetector_NoUnfinalizedBlocks(t *testing.T) {
	t.Parallel()

	adapter := &fakeReorgAwareAdapter{
		chainName:    "base",
		blockHashes:  map[int64]string{},
		parentHashes: map[int64]string{},
	}
	blockRepo := &fakeBlockRepo{unfinalizedBlocks: nil}
	reorgCh := make(chan event.ReorgEvent, 1)

	d := New(model.ChainBase, model.NetworkSepolia, adapter, blockRepo, reorgCh, time.Second, slog.Default())

	err := d.check(context.Background())
	require.NoError(t, err)

	select {
	case <-reorgCh:
		t.Fatal("should not have sent reorg event")
	default:
	}
}

func TestDetector_AllHashesMatch(t *testing.T) {
	t.Parallel()

	adapter := &fakeReorgAwareAdapter{
		chainName: "base",
		blockHashes: map[int64]string{
			100: "0xaaa",
			101: "0xbbb",
		},
		parentHashes: map[int64]string{
			100: "0x999",
			101: "0xaaa",
		},
	}
	blockRepo := &fakeBlockRepo{
		unfinalizedBlocks: []model.IndexedBlock{
			{Chain: model.ChainBase, Network: model.NetworkSepolia, BlockNumber: 100, BlockHash: "0xaaa"},
			{Chain: model.ChainBase, Network: model.NetworkSepolia, BlockNumber: 101, BlockHash: "0xbbb"},
		},
	}
	reorgCh := make(chan event.ReorgEvent, 1)

	d := New(model.ChainBase, model.NetworkSepolia, adapter, blockRepo, reorgCh, time.Second, slog.Default())

	err := d.check(context.Background())
	require.NoError(t, err)

	select {
	case <-reorgCh:
		t.Fatal("should not have sent reorg event")
	default:
	}
}

func TestDetector_HashMismatch_SendsReorgEvent(t *testing.T) {
	t.Parallel()

	adapter := &fakeReorgAwareAdapter{
		chainName: "base",
		blockHashes: map[int64]string{
			100: "0xaaa",
			101: "0xNEW_HASH",
		},
		parentHashes: map[int64]string{
			100: "0x999",
			101: "0xaaa",
		},
	}
	blockRepo := &fakeBlockRepo{
		unfinalizedBlocks: []model.IndexedBlock{
			{Chain: model.ChainBase, Network: model.NetworkSepolia, BlockNumber: 100, BlockHash: "0xaaa"},
			{Chain: model.ChainBase, Network: model.NetworkSepolia, BlockNumber: 101, BlockHash: "0xOLD_HASH"},
		},
	}
	reorgCh := make(chan event.ReorgEvent, 1)

	d := New(model.ChainBase, model.NetworkSepolia, adapter, blockRepo, reorgCh, time.Second, slog.Default())

	err := d.check(context.Background())
	require.NoError(t, err)

	select {
	case reorg := <-reorgCh:
		assert.Equal(t, model.ChainBase, reorg.Chain)
		assert.Equal(t, model.NetworkSepolia, reorg.Network)
		assert.Equal(t, int64(101), reorg.ForkBlockNumber)
		assert.Equal(t, "0xOLD_HASH", reorg.ExpectedHash)
		assert.Equal(t, "0xNEW_HASH", reorg.ActualHash)
	default:
		t.Fatal("expected reorg event")
	}
}

// TestDetector_ConsecutiveRPCErrors_AlertAfterThreshold verifies that the
// reorg detector sends an alert after 5 consecutive RPC errors.
func TestDetector_ConsecutiveRPCErrors_AlertAfterThreshold(t *testing.T) {
	t.Parallel()

	// Adapter always returns error
	adapter := &fakeReorgAwareAdapter{
		chainName: "base",
		hashErr:   fmt.Errorf("rpc unavailable"),
	}

	// 6 blocks → 6 RPC calls, all will fail
	blocks := make([]model.IndexedBlock, 6)
	for i := range blocks {
		blocks[i] = model.IndexedBlock{
			Chain:       model.ChainBase,
			Network:     model.NetworkSepolia,
			BlockNumber: int64(100 + i),
			BlockHash:   fmt.Sprintf("0x%d", i),
		}
	}
	blockRepo := &fakeBlockRepo{unfinalizedBlocks: blocks}
	reorgCh := make(chan event.ReorgEvent, 1)

	var alertsSent int
	fakeAlerter := &countingAlerter{sent: &alertsSent}

	d := New(model.ChainBase, model.NetworkSepolia, adapter, blockRepo, reorgCh, time.Second, slog.Default())
	d.WithAlerter(fakeAlerter)

	err := d.check(context.Background())
	require.NoError(t, err)

	// With 6 blocks failing, consecutiveRPCErrs reaches 5 at block[4], then 6 at block[5].
	// Alert should be sent at least once (when threshold is first reached or exceeded).
	assert.GreaterOrEqual(t, alertsSent, 1, "Alert should have been sent after 5+ consecutive RPC errors")
	assert.Equal(t, 6, d.consecutiveRPCErrs, "Should have 6 consecutive RPC errors")
}

// TestDetector_RPCErrorCountResetsOnSuccess verifies that a successful RPC call
// resets the consecutive error counter.
func TestDetector_RPCErrorCountResetsOnSuccess(t *testing.T) {
	t.Parallel()

	adapter := &fakeReorgAwareAdapter{
		chainName: "base",
		blockHashes: map[int64]string{
			103: "0xaaa",
		},
		parentHashes: map[int64]string{
			103: "0x999",
		},
		// blocks 100-102 missing → error; 103 succeeds
	}

	blocks := []model.IndexedBlock{
		{Chain: model.ChainBase, Network: model.NetworkSepolia, BlockNumber: 100, BlockHash: "0xa"},
		{Chain: model.ChainBase, Network: model.NetworkSepolia, BlockNumber: 101, BlockHash: "0xb"},
		{Chain: model.ChainBase, Network: model.NetworkSepolia, BlockNumber: 102, BlockHash: "0xc"},
		{Chain: model.ChainBase, Network: model.NetworkSepolia, BlockNumber: 103, BlockHash: "0xaaa"},
	}
	blockRepo := &fakeBlockRepo{unfinalizedBlocks: blocks}
	reorgCh := make(chan event.ReorgEvent, 1)

	d := New(model.ChainBase, model.NetworkSepolia, adapter, blockRepo, reorgCh, time.Second, slog.Default())

	err := d.check(context.Background())
	require.NoError(t, err)

	// Block 103 succeeded, so counter should be reset to 0
	assert.Equal(t, 0, d.consecutiveRPCErrs, "Successful RPC should reset consecutive error count")
}

func TestDetector_RPCError_ContinuesChecking(t *testing.T) {
	t.Parallel()

	adapter := &fakeReorgAwareAdapter{
		chainName: "base",
		blockHashes: map[int64]string{
			100: "0xaaa",
			// 101 missing → will error
			102: "0xNEW",
		},
		parentHashes: map[int64]string{
			100: "0x999",
			102: "0xbbb",
		},
	}
	blockRepo := &fakeBlockRepo{
		unfinalizedBlocks: []model.IndexedBlock{
			{Chain: model.ChainBase, Network: model.NetworkSepolia, BlockNumber: 100, BlockHash: "0xaaa"},
			{Chain: model.ChainBase, Network: model.NetworkSepolia, BlockNumber: 101, BlockHash: "0xbbb"},
			{Chain: model.ChainBase, Network: model.NetworkSepolia, BlockNumber: 102, BlockHash: "0xOLD"},
		},
	}
	reorgCh := make(chan event.ReorgEvent, 1)

	d := New(model.ChainBase, model.NetworkSepolia, adapter, blockRepo, reorgCh, time.Second, slog.Default())

	err := d.check(context.Background())
	require.NoError(t, err)

	// Should detect mismatch at 102 (skipping 101 which errored)
	select {
	case reorg := <-reorgCh:
		assert.Equal(t, int64(102), reorg.ForkBlockNumber)
	default:
		t.Fatal("expected reorg event for block 102")
	}
}
