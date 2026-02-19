package reorgdetector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	chainpkg "github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
func (f *fakeBlockRepo) DeleteFromBlockTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ int64) error {
	return nil
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
