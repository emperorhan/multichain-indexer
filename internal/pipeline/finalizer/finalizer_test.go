package finalizer

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
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

type fakeBlockRepo struct{}

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
func (f *fakeBlockRepo) DeleteFromBlockTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ int64) error {
	return nil
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
