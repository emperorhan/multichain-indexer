package pipeline

import (
	"context"
	"log/slog"
	"testing"
	"time"

	chainmocks "github.com/kodax/koda-custody-indexer/internal/chain/mocks"
	"github.com/kodax/koda-custody-indexer/internal/domain/model"
	storemocks "github.com/kodax/koda-custody-indexer/internal/store/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestNew_SmokeTest(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
	mockDB := storemocks.NewMockTxBeginner(ctrl)

	cfg := Config{
		Chain:             model.ChainSolana,
		Network:           model.NetworkDevnet,
		BatchSize:         100,
		IndexingInterval:  5 * time.Second,
		FetchWorkers:      2,
		NormalizerWorkers: 2,
		ChannelBufferSize: 10,
		SidecarAddr:       "localhost:50051",
		SidecarTimeout:    30 * time.Second,
	}

	repos := &Repos{
		WatchedAddr: storemocks.NewMockWatchedAddressRepository(ctrl),
		Cursor:      storemocks.NewMockCursorRepository(ctrl),
		Transaction: storemocks.NewMockTransactionRepository(ctrl),
		Transfer:    storemocks.NewMockTransferRepository(ctrl),
		Balance:     storemocks.NewMockBalanceRepository(ctrl),
		Token:       storemocks.NewMockTokenRepository(ctrl),
		Config:      storemocks.NewMockIndexerConfigRepository(ctrl),
	}

	p := New(cfg, mockAdapter, mockDB, repos, slog.Default())
	require.NotNil(t, p)
}

func TestPipeline_Run_ImmediateCancel(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
	mockDB := storemocks.NewMockTxBeginner(ctrl)

	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]model.WatchedAddress{}, nil).AnyTimes()

	cfg := Config{
		Chain:             model.ChainSolana,
		Network:           model.NetworkDevnet,
		BatchSize:         100,
		IndexingInterval:  5 * time.Second,
		FetchWorkers:      1,
		NormalizerWorkers: 1,
		ChannelBufferSize: 10,
		SidecarAddr:       "localhost:50051",
		SidecarTimeout:    30 * time.Second,
	}

	repos := &Repos{
		WatchedAddr: mockWatchedAddr,
		Cursor:      storemocks.NewMockCursorRepository(ctrl),
		Transaction: storemocks.NewMockTransactionRepository(ctrl),
		Transfer:    storemocks.NewMockTransferRepository(ctrl),
		Balance:     storemocks.NewMockBalanceRepository(ctrl),
		Token:       storemocks.NewMockTokenRepository(ctrl),
		Config:      storemocks.NewMockIndexerConfigRepository(ctrl),
	}

	p := New(cfg, mockAdapter, mockDB, repos, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := p.Run(ctx)
	// Should return context.Canceled from one of the stages
	assert.ErrorIs(t, err, context.Canceled)
}
