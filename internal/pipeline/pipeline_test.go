package pipeline

import (
	"context"
	"log/slog"
	"testing"
	"time"

	chainmocks "github.com/emperorhan/multichain-indexer/internal/chain/mocks"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/replay"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
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
		WatchedAddr:  storemocks.NewMockWatchedAddressRepository(ctrl),
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Watermark:    storemocks.NewMockWatermarkRepository(ctrl),
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
		WatchedAddr:  mockWatchedAddr,
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Watermark:    storemocks.NewMockWatermarkRepository(ctrl),
	}

	p := New(cfg, mockAdapter, mockDB, repos, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := p.Run(ctx)
	// Should return context.Canceled from one of the stages
	assert.ErrorIs(t, err, context.Canceled)
}

func TestCalcRestartBackoff(t *testing.T) {
	p := &Pipeline{}

	// 0 failures → 1s
	p.consecutiveFailures = 0
	assert.Equal(t, 1*time.Second, p.calcRestartBackoff())

	// 1 failure → 2s
	p.consecutiveFailures = 1
	assert.Equal(t, 2*time.Second, p.calcRestartBackoff())

	// 2 failures → 4s
	p.consecutiveFailures = 2
	assert.Equal(t, 4*time.Second, p.calcRestartBackoff())

	// 3 failures → 8s
	p.consecutiveFailures = 3
	assert.Equal(t, 8*time.Second, p.calcRestartBackoff())

	// Large value → capped at 5 minutes
	p.consecutiveFailures = 20
	assert.Equal(t, maxRestartBackoff, p.calcRestartBackoff())
}

func TestDrainChannels(t *testing.T) {
	p := &Pipeline{
		jobCh:        make(chan event.FetchJob, 5),
		rawBatchCh:   make(chan event.RawBatch, 5),
		normalizedCh: make(chan event.NormalizedBatch, 5),
	}

	// Fill channels with test data.
	p.jobCh <- event.FetchJob{Address: "a"}
	p.jobCh <- event.FetchJob{Address: "b"}
	p.rawBatchCh <- event.RawBatch{}
	p.normalizedCh <- event.NormalizedBatch{}
	p.normalizedCh <- event.NormalizedBatch{}

	assert.Equal(t, 2, len(p.jobCh))
	assert.Equal(t, 1, len(p.rawBatchCh))
	assert.Equal(t, 2, len(p.normalizedCh))

	p.drainChannels()

	assert.Equal(t, 0, len(p.jobCh))
	assert.Equal(t, 0, len(p.rawBatchCh))
	assert.Equal(t, 0, len(p.normalizedCh))
}

func TestPipeline_DeactivateActivate(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
	mockDB := storemocks.NewMockTxBeginner(ctrl)

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
		WatchedAddr:  storemocks.NewMockWatchedAddressRepository(ctrl),
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Watermark:    storemocks.NewMockWatermarkRepository(ctrl),
	}

	p := New(cfg, mockAdapter, mockDB, repos, slog.Default())
	require.True(t, p.IsActive())

	// Deactivate → verify inactive state.
	p.Deactivate()
	assert.False(t, p.IsActive())
	assert.Equal(t, string(HealthStatusInactive), p.Health().Snapshot().Status)

	// Double deactivate is a no-op.
	p.Deactivate()
	assert.False(t, p.IsActive())

	// Activate → verify active again.
	p.Activate()
	assert.True(t, p.IsActive())

	// Double activate is a no-op.
	p.Activate()
	assert.True(t, p.IsActive())
}

func TestPipeline_Run_DeactivatedThenCancel(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
	mockDB := storemocks.NewMockTxBeginner(ctrl)

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
		WatchedAddr:  storemocks.NewMockWatchedAddressRepository(ctrl),
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Watermark:    storemocks.NewMockWatermarkRepository(ctrl),
	}

	p := New(cfg, mockAdapter, mockDB, repos, slog.Default())

	// Deactivate before Run → Run should block at the wait loop.
	p.Deactivate()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- p.Run(ctx)
	}()

	// Give Run() a moment to enter the wait loop.
	time.Sleep(50 * time.Millisecond)

	// Cancel should unblock the wait.
	cancel()
	err := <-done
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPipeline_RequestReplay_ContextCancel(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
	mockDB := storemocks.NewMockTxBeginner(ctrl)

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
		WatchedAddr:  storemocks.NewMockWatchedAddressRepository(ctrl),
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Watermark:    storemocks.NewMockWatermarkRepository(ctrl),
	}

	p := New(cfg, mockAdapter, mockDB, repos, slog.Default())

	// Fill the replay channel so next send blocks.
	p.replayCh <- replayOp{}

	// RequestReplay with already-cancelled context should return immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.RequestReplay(ctx, replay.PurgeRequest{})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestNew_ChannelBufferSizes(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
	mockDB := storemocks.NewMockTxBeginner(ctrl)

	repos := &Repos{
		WatchedAddr:  storemocks.NewMockWatchedAddressRepository(ctrl),
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Watermark:    storemocks.NewMockWatermarkRepository(ctrl),
	}

	t.Run("defaults", func(t *testing.T) {
		cfg := Config{
			Chain: model.ChainSolana, Network: model.NetworkDevnet,
			FetchWorkers: 1, NormalizerWorkers: 1,
			SidecarAddr: "localhost:50051", SidecarTimeout: 30 * time.Second,
		}
		p := New(cfg, mockAdapter, mockDB, repos, slog.Default())
		assert.Equal(t, 20, cap(p.jobCh))
		assert.Equal(t, 10, cap(p.rawBatchCh))
		assert.Equal(t, 5, cap(p.normalizedCh))
	})

	t.Run("global_fallback", func(t *testing.T) {
		cfg := Config{
			Chain: model.ChainSolana, Network: model.NetworkDevnet,
			FetchWorkers: 1, NormalizerWorkers: 1,
			ChannelBufferSize: 50,
			SidecarAddr: "localhost:50051", SidecarTimeout: 30 * time.Second,
		}
		p := New(cfg, mockAdapter, mockDB, repos, slog.Default())
		assert.Equal(t, 50, cap(p.jobCh))
		assert.Equal(t, 50, cap(p.rawBatchCh))
		assert.Equal(t, 50, cap(p.normalizedCh))
	})

	t.Run("per_stage_override", func(t *testing.T) {
		cfg := Config{
			Chain: model.ChainSolana, Network: model.NetworkDevnet,
			FetchWorkers: 1, NormalizerWorkers: 1,
			ChannelBufferSize:      50,
			JobChBufferSize:        30,
			RawBatchChBufferSize:   15,
			NormalizedChBufferSize: 8,
			SidecarAddr: "localhost:50051", SidecarTimeout: 30 * time.Second,
		}
		p := New(cfg, mockAdapter, mockDB, repos, slog.Default())
		assert.Equal(t, 30, cap(p.jobCh))
		assert.Equal(t, 15, cap(p.rawBatchCh))
		assert.Equal(t, 8, cap(p.normalizedCh))
	})
}

func TestPipeline_BackfillCh_Created(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
	mockDB := storemocks.NewMockTxBeginner(ctrl)

	repos := &Repos{
		WatchedAddr:  storemocks.NewMockWatchedAddressRepository(ctrl),
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Watermark:    storemocks.NewMockWatermarkRepository(ctrl),
	}

	cfg := Config{
		Chain: model.ChainBase, Network: model.NetworkMainnet,
		FetchWorkers: 1, NormalizerWorkers: 1,
		SidecarAddr: "localhost:50051", SidecarTimeout: 30 * time.Second,
	}

	p := New(cfg, mockAdapter, mockDB, repos, slog.Default())
	require.NotNil(t, p.backfillCh)
	assert.Equal(t, 1, cap(p.backfillCh))

	// Verify non-blocking send works.
	p.backfillCh <- coordinator.BackfillRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 1000,
	}
	req := <-p.backfillCh
	assert.Equal(t, int64(1000), req.FromBlock)
}

func TestPipeline_SetReplayService(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
	mockDB := storemocks.NewMockTxBeginner(ctrl)

	repos := &Repos{
		WatchedAddr:  storemocks.NewMockWatchedAddressRepository(ctrl),
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Watermark:    storemocks.NewMockWatermarkRepository(ctrl),
	}

	cfg := Config{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		FetchWorkers: 1, NormalizerWorkers: 1,
		SidecarAddr: "localhost:50051", SidecarTimeout: 30 * time.Second,
	}

	p := New(cfg, mockAdapter, mockDB, repos, slog.Default())
	assert.Nil(t, p.ReplayService())

	// SetReplayService is tested indirectly; verify accessor round-trip.
	p.SetReplayService(nil)
	assert.Nil(t, p.ReplayService())
}
