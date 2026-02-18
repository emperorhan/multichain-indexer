package pipeline

import (
	"errors"
	"context"
	"log/slog"
	"testing"
	"time"

	chainmocks "github.com/emperorhan/multichain-indexer/internal/chain/mocks"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	redispkg "github.com/emperorhan/multichain-indexer/internal/store/redis"
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
		Cursor:       storemocks.NewMockCursorRepository(ctrl),
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Config:       storemocks.NewMockIndexerConfigRepository(ctrl),
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
		Cursor:       storemocks.NewMockCursorRepository(ctrl),
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Config:       storemocks.NewMockIndexerConfigRepository(ctrl),
	}

	p := New(cfg, mockAdapter, mockDB, repos, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := p.Run(ctx)
	// Should return context.Canceled from one of the stages
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPipeline_Run_StreamTransportRequiresBackend(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
	mockDB := storemocks.NewMockTxBeginner(ctrl)

	mockWatchedAddr := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockWatchedAddr.EXPECT().
		GetActive(gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]model.WatchedAddress{}, nil).AnyTimes()

	cfg := Config{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		BatchSize:              100,
		IndexingInterval:       5 * time.Second,
		FetchWorkers:           1,
		NormalizerWorkers:      1,
		ChannelBufferSize:      10,
		SidecarAddr:            "localhost:50051",
		SidecarTimeout:         30 * time.Second,
		StreamTransportEnabled: true,
		StreamNamespace:        "pipeline",
		StreamSessionID:        "test-session",
	}

	repos := &Repos{
		WatchedAddr:  mockWatchedAddr,
		Cursor:       storemocks.NewMockCursorRepository(ctrl),
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Config:       storemocks.NewMockIndexerConfigRepository(ctrl),
	}

	p := New(cfg, mockAdapter, mockDB, repos, slog.Default())
	err := p.Run(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stream transport enabled but stream backend is not configured")
}

func TestPipeline_Run_StreamBoundaryName(t *testing.T) {
	p := New(
		Config{
			Chain:           model.ChainBTC,
			Network:         model.NetworkTestnet,
			StreamNamespace: "indexer-bus",
			StreamSessionID: "session-1",
		},
		nil,
		nil,
		nil,
		slog.Default(),
	)

	got := p.streamBoundaryName("fetcher-normalizer")
	assert.Equal(t, "indexer-bus:chain=btc:network=testnet:session=session-1:boundary=fetcher-normalizer", got)
}

func TestPipeline_Run_RawBatchTransportParity_MandatoryChains_StreamVsInMemory(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockRepo := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockRepo.EXPECT().
		GetActive(gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]model.WatchedAddress{}, nil).AnyTimes()

	repos := &Repos{
		WatchedAddr:  mockRepo,
		Cursor:       storemocks.NewMockCursorRepository(ctrl),
		Transaction:  storemocks.NewMockTransactionRepository(ctrl),
		BalanceEvent: storemocks.NewMockBalanceEventRepository(ctrl),
		Balance:      storemocks.NewMockBalanceRepository(ctrl),
		Token:        storemocks.NewMockTokenRepository(ctrl),
		Config:       storemocks.NewMockIndexerConfigRepository(ctrl),
	}

	solanaWallet := "wallet-solana"
	baseWallet := "wallet-base"
	btcWallet := "wallet-btc"
	rawBatches := []event.RawBatch{
		{
			Chain: model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "SolanaAddr",
			WalletID: &solanaWallet,
		},
		{
			Chain: model.ChainBase,
			Network: model.NetworkSepolia,
			Address: "BaseAddr",
			WalletID: &baseWallet,
		},
		{
			Chain: model.ChainBTC,
			Network: model.NetworkTestnet,
			Address: "BtcAddr",
			WalletID: &btcWallet,
		},
	}

	stream := redispkg.NewInMemoryStream()
	cfg := Config{
		Chain:                model.ChainSolana,
		Network:              model.NetworkDevnet,
		FetchWorkers:         1,
		NormalizerWorkers:    1,
		ChannelBufferSize:    len(rawBatches),
		StreamBackend:        stream,
		StreamNamespace:      "pipeline",
		StreamSessionID:      "parity-session",
		StreamTransportEnabled: true,
	}
	streamPipeline := New(cfg, mockAdapter, mockDB, repos, slog.Default())

	inMemoryCfg := cfg
	inMemoryCfg.StreamTransportEnabled = false
	inMemoryPipeline := New(inMemoryCfg, mockAdapter, mockDB, repos, slog.Default())

	streamOutput := collectRawBatchesThroughTransport(t, streamPipeline, rawBatches)
	inMemoryOutput := collectRawBatchesThroughMemoryBoundary(t, inMemoryPipeline, rawBatches)

	assert.Equal(t, rawBatches, streamOutput)
	assert.Equal(t, rawBatches, inMemoryOutput)
	assert.Equal(t, streamOutput, inMemoryOutput)
}

func collectRawBatchesThroughTransport(t *testing.T, p *Pipeline, rawBatches []event.RawBatch) []event.RawBatch {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streamName := p.streamBoundaryName(streamBoundaryFetchToNormal)
	fetchBoundaryCh := make(chan event.RawBatch, len(rawBatches))
	normalizerInCh := make(chan event.RawBatch, len(rawBatches))

	runErr := make(chan error, 2)
	go func() {
		runErr <- p.runRawBatchStreamProducer(ctx, fetchBoundaryCh, p.cfg.StreamBackend, streamName)
	}()
	go func() {
		runErr <- p.runRawBatchStreamConsumer(ctx, normalizerInCh, p.cfg.StreamBackend, streamName)
	}()

	for _, batch := range rawBatches {
		fetchBoundaryCh <- batch
	}
	close(fetchBoundaryCh)

	got := make([]event.RawBatch, 0, len(rawBatches))
	for i := 0; i < len(rawBatches); i++ {
		got = append(got, <-normalizerInCh)
	}
	cancel()

	for i := 0; i < 2; i++ {
		err := <-runErr
		assert.True(t, err == nil || errors.Is(err, context.Canceled))
	}

	return got
}

func collectRawBatchesThroughMemoryBoundary(t *testing.T, p *Pipeline, rawBatches []event.RawBatch) []event.RawBatch {
	t.Helper()

	fetchBoundaryCh := make(chan event.RawBatch, len(rawBatches))
	normalizerInCh := make(chan event.RawBatch, len(rawBatches))
	finished := make(chan struct{})

	go func() {
		defer close(finished)
		defer close(normalizerInCh)
		for batch := range fetchBoundaryCh {
			normalizerInCh <- batch
		}
	}()

	for _, batch := range rawBatches {
		fetchBoundaryCh <- batch
	}
	close(fetchBoundaryCh)
	<-finished

	got := make([]event.RawBatch, 0, len(rawBatches))
	for i := 0; i < len(rawBatches); i++ {
		got = append(got, <-normalizerInCh)
	}

	return got
}
