package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	chainmocks "github.com/emperorhan/multichain-indexer/internal/chain/mocks"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestProcessJob_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

	rawBatchCh := make(chan event.RawBatch, 1)
	f := &Fetcher{
		adapter:    mockAdapter,
		rawBatchCh: rawBatchCh,
		logger:     slog.Default(),
	}

	walletID := "wallet-1"
	orgID := "org-1"
	cursor := "prevSig"

	job := event.FetchJob{
		Chain:          model.ChainSolana,
		Network:        model.NetworkDevnet,
		Address:        "addr1",
		CursorValue:    &cursor,
		CursorSequence: 42,
		BatchSize:      100,
		WalletID:       &walletID,
		OrgID:          &orgID,
	}

	now := time.Now()
	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), "addr1", &cursor, 100).
		Return([]chain.SignatureInfo{
			{Hash: "sig1", Sequence: 100, Time: &now},
			{Hash: "sig2", Sequence: 200, Time: &now},
			{Hash: "sig3", Sequence: 300, Time: &now},
		}, nil)

	mockAdapter.EXPECT().
		FetchTransactions(gomock.Any(), []string{"sig1", "sig2", "sig3"}).
		Return([]json.RawMessage{
			json.RawMessage(`{"tx":1}`),
			json.RawMessage(`{"tx":2}`),
			json.RawMessage(`{"tx":3}`),
		}, nil)

	err := f.processJob(context.Background(), slog.Default(), job)
	require.NoError(t, err)

	batch := <-rawBatchCh
	assert.Equal(t, model.ChainSolana, batch.Chain)
	assert.Equal(t, model.NetworkDevnet, batch.Network)
	assert.Equal(t, "addr1", batch.Address)
	assert.Equal(t, &cursor, batch.PreviousCursorValue)
	assert.Equal(t, int64(42), batch.PreviousCursorSequence)
	assert.Equal(t, &walletID, batch.WalletID)
	assert.Equal(t, &orgID, batch.OrgID)
	assert.Len(t, batch.RawTransactions, 3)
	assert.Len(t, batch.Signatures, 3)
	require.NotNil(t, batch.NewCursorValue)
	assert.Equal(t, "sig3", *batch.NewCursorValue) // newest = last
	assert.Equal(t, int64(300), batch.NewCursorSequence)
}

func TestProcessJob_NoSignatures(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

	rawBatchCh := make(chan event.RawBatch, 1)
	f := &Fetcher{
		adapter:    mockAdapter,
		rawBatchCh: rawBatchCh,
		logger:     slog.Default(),
	}

	job := event.FetchJob{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		Address:   "addr1",
		BatchSize: 100,
	}

	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), "addr1", (*string)(nil), 100).
		Return([]chain.SignatureInfo{}, nil)

	err := f.processJob(context.Background(), slog.Default(), job)
	require.NoError(t, err)

	select {
	case <-rawBatchCh:
		t.Fatal("expected no batch")
	default:
	}
}

func TestProcessJob_FetchSignaturesError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

	f := &Fetcher{
		adapter: mockAdapter,
		logger:  slog.Default(),
	}

	job := event.FetchJob{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		Address:   "addr1",
		BatchSize: 100,
	}

	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), "addr1", (*string)(nil), 100).
		Return(nil, errors.New("rpc timeout"))

	err := f.processJob(context.Background(), slog.Default(), job)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpc timeout")
}

func TestProcessJob_FetchTransactionsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

	f := &Fetcher{
		adapter: mockAdapter,
		logger:  slog.Default(),
	}

	job := event.FetchJob{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		Address:   "addr1",
		BatchSize: 100,
	}

	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), "addr1", (*string)(nil), 100).
		Return([]chain.SignatureInfo{
			{Hash: "sig1", Sequence: 100},
		}, nil)

	mockAdapter.EXPECT().
		FetchTransactions(gomock.Any(), []string{"sig1"}).
		Return(nil, errors.New("network error"))

	err := f.processJob(context.Background(), slog.Default(), job)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network error")
}

func TestFetcher_Run_ContextCancel(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

	jobCh := make(chan event.FetchJob)
	rawBatchCh := make(chan event.RawBatch)

	f := New(mockAdapter, jobCh, rawBatchCh, 1, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := f.Run(ctx)
	assert.Equal(t, context.Canceled, err)
}
