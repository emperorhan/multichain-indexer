package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	chainmocks "github.com/emperorhan/multichain-indexer/internal/chain/mocks"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	redispkg "github.com/emperorhan/multichain-indexer/internal/store/redis"
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
	assert.Equal(t, "indexer-bus:chain=btc:network=testnet:boundary=fetcher-normalizer", got)
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
			Chain:    model.ChainSolana,
			Network:  model.NetworkDevnet,
			Address:  "SolanaAddr",
			WalletID: &solanaWallet,
		},
		{
			Chain:    model.ChainBase,
			Network:  model.NetworkSepolia,
			Address:  "BaseAddr",
			WalletID: &baseWallet,
		},
		{
			Chain:    model.ChainBTC,
			Network:  model.NetworkTestnet,
			Address:  "BtcAddr",
			WalletID: &btcWallet,
		},
	}

	stream := redispkg.NewInMemoryStream()
	cfg := Config{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		FetchWorkers:           1,
		NormalizerWorkers:      1,
		ChannelBufferSize:      len(rawBatches),
		StreamBackend:          stream,
		StreamNamespace:        "pipeline",
		StreamSessionID:        "parity-session",
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

func TestPipeline_Run_RawBatchStreamConsumer_ResumeFromPersistedCheckpoint(t *testing.T) {
	testCases := []struct {
		name    string
		chain   model.Chain
		network model.Network
	}{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			checkpointStream := newCheckpointInMemoryStream()

			cfg := Config{
				Chain:                  tc.chain,
				Network:                tc.network,
				FetchWorkers:           1,
				NormalizerWorkers:      1,
				ChannelBufferSize:      2,
				StreamBackend:          checkpointStream,
				StreamNamespace:        "pipeline",
				StreamSessionID:        "session-a",
				StreamTransportEnabled: true,
			}
			p := New(cfg, nil, nil, &Repos{}, slog.Default())

			streamName := p.streamBoundaryName(streamBoundaryFetchToNormal)
			firstID, err := checkpointStream.PublishJSON(context.Background(), streamName, event.RawBatch{Chain: tc.chain, Network: tc.network, Address: "A"})
			require.NoError(t, err)
			secondID, err := checkpointStream.PublishJSON(context.Background(), streamName, event.RawBatch{Chain: tc.chain, Network: tc.network, Address: "B"})
			require.NoError(t, err)

			checkpointKey := p.streamBoundaryCheckpointKey(streamBoundaryFetchToNormal)
			require.NoError(t, checkpointStream.PersistStreamCheckpoint(context.Background(), checkpointKey, firstID))

			got := collectRawBatchesThroughStreamBoundary(t, p, checkpointStream, 1)
			require.Equal(t, []event.RawBatch{{Chain: tc.chain, Network: tc.network, Address: "B"}}, got)

			storedCheckpoint, err := checkpointStream.LoadStreamCheckpoint(context.Background(), checkpointKey)
			require.NoError(t, err)
			assert.Equal(t, secondID, storedCheckpoint)
		})
	}
}

func TestPipeline_Run_RawBatchStreamConsumer_MissingCheckpointBootstrapsFromStart(t *testing.T) {
	testCases := []struct {
		name    string
		chain   model.Chain
		network model.Network
	}{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			checkpointStream := newCheckpointInMemoryStream()
			cfg := Config{
				Chain:                  tc.chain,
				Network:                tc.network,
				FetchWorkers:           1,
				NormalizerWorkers:      1,
				ChannelBufferSize:      2,
				StreamBackend:          checkpointStream,
				StreamNamespace:        "pipeline",
				StreamSessionID:        "session-a",
				StreamTransportEnabled: true,
			}
			p := New(cfg, nil, nil, &Repos{}, slog.Default())
			streamName := p.streamBoundaryName(streamBoundaryFetchToNormal)

			rawBatches := []event.RawBatch{
				{Chain: tc.chain, Network: tc.network, Address: "A"},
				{Chain: tc.chain, Network: tc.network, Address: "B"},
			}
			for _, batch := range rawBatches {
				_, err := checkpointStream.PublishJSON(context.Background(), streamName, batch)
				require.NoError(t, err)
			}

			output := collectRawBatchesThroughStreamBoundary(t, p, checkpointStream, len(rawBatches))
			assert.Equal(t, rawBatches, output)
		})
	}
}

func TestPipeline_Run_RawBatchStreamConsumer_InvalidCheckpointFallsBackToBootstrap(t *testing.T) {
	testCases := []struct {
		name    string
		chain   model.Chain
		network model.Network
	}{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			checkpointStream := newCheckpointInMemoryStream()
			cfg := Config{
				Chain:                  tc.chain,
				Network:                tc.network,
				FetchWorkers:           1,
				NormalizerWorkers:      1,
				ChannelBufferSize:      2,
				StreamBackend:          checkpointStream,
				StreamNamespace:        "pipeline",
				StreamSessionID:        "session-a",
				StreamTransportEnabled: true,
			}

			p := New(cfg, nil, nil, &Repos{}, slog.Default())
			streamName := p.streamBoundaryName(streamBoundaryFetchToNormal)
			rawBatches := []event.RawBatch{
				{Chain: tc.chain, Network: tc.network, Address: "A"},
				{Chain: tc.chain, Network: tc.network, Address: "B"},
			}

			checkpointKey := p.streamBoundaryCheckpointKey(streamBoundaryFetchToNormal)
			require.NoError(t, checkpointStream.PersistStreamCheckpoint(context.Background(), checkpointKey, "invalid-checkpoint"))

			for _, batch := range rawBatches {
				_, err := checkpointStream.PublishJSON(context.Background(), streamName, batch)
				require.NoError(t, err)
			}

			output := collectRawBatchesThroughStreamBoundary(t, p, checkpointStream, len(rawBatches))
			assert.Equal(t, rawBatches, output)
		})
	}
}

func TestPipeline_Run_StreamBoundaryCheckpointKey_DeterministicAndScopedByChainNetworkSessionBoundary(t *testing.T) {
	testCases := []struct {
		name     string
		chain    model.Chain
		network  model.Network
		session  string
		boundary string
		expected string
	}{
		{
			name:     "solana-devnet-fetcher-normalizer-session-alpha",
			chain:    model.ChainSolana,
			network:  model.NetworkDevnet,
			session:  "session-alpha",
			boundary: streamBoundaryFetchToNormal,
			expected: "stream-checkpoint:chain=solana:network=devnet:session=session-alpha:boundary=fetcher-normalizer",
		},
		{
			name:     "base-sepolia-fetcher-normalizer-session-beta",
			chain:    model.ChainBase,
			network:  model.NetworkSepolia,
			session:  "session-beta",
			boundary: streamBoundaryFetchToNormal,
			expected: "stream-checkpoint:chain=base:network=sepolia:session=session-beta:boundary=fetcher-normalizer",
		},
		{
			name:     "btc-testnet-fetcher-normalizer-session-gamma",
			chain:    model.ChainBTC,
			network:  model.NetworkTestnet,
			session:  "session-gamma",
			boundary: streamBoundaryFetchToNormal,
			expected: "stream-checkpoint:chain=btc:network=testnet:session=session-gamma:boundary=fetcher-normalizer",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := New(
				Config{
					Chain:           tc.chain,
					Network:         tc.network,
					StreamSessionID: tc.session,
				},
				nil,
				nil,
				nil,
				slog.Default(),
			)

			got := p.streamBoundaryCheckpointKey(tc.boundary)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestPipeline_Run_StreamBoundaryCheckpointKey_DefaultSession(t *testing.T) {
	p := New(
		Config{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
		},
		nil,
		nil,
		nil,
		slog.Default(),
	)

	got := p.streamBoundaryCheckpointKey(streamBoundaryFetchToNormal)
	assert.Equal(t, "stream-checkpoint:chain=solana:network=devnet:session=default:boundary=fetcher-normalizer", got)
}

func TestPipeline_Run_RawBatchStreamConsumer_ResumeFromLegacyCheckpoint(t *testing.T) {
	testCases := []struct {
		name    string
		chain   model.Chain
		network model.Network
	}{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			checkpointStream := newCheckpointInMemoryStream()
			cfg := Config{
				Chain:                  tc.chain,
				Network:                tc.network,
				FetchWorkers:           1,
				NormalizerWorkers:      1,
				ChannelBufferSize:      2,
				StreamBackend:          checkpointStream,
				StreamNamespace:        "pipeline",
				StreamSessionID:        "session-a",
				StreamTransportEnabled: true,
			}
			p := New(cfg, nil, nil, &Repos{}, slog.Default())

			streamName := p.streamBoundaryName(streamBoundaryFetchToNormal)
			rawBatches := []event.RawBatch{
				{Chain: tc.chain, Network: tc.network, Address: "A"},
				{Chain: tc.chain, Network: tc.network, Address: "B"},
			}

			firstID, err := checkpointStream.PublishJSON(context.Background(), streamName, rawBatches[0])
			require.NoError(t, err)
			_, err = checkpointStream.PublishJSON(context.Background(), streamName, rawBatches[1])
			require.NoError(t, err)

			legacyCheckpointKey := p.streamBoundaryLegacyCheckpointKey(streamBoundaryFetchToNormal)
			require.NoError(t, checkpointStream.PersistStreamCheckpoint(context.Background(), legacyCheckpointKey, firstID))

			output := collectRawBatchesThroughStreamBoundary(t, p, checkpointStream, 1)
			assert.Equal(t, rawBatches[1:], output)

			checkpointKey := p.streamBoundaryCheckpointKey(streamBoundaryFetchToNormal)
			storedCheckpoint, err := checkpointStream.LoadStreamCheckpoint(context.Background(), checkpointKey)
			require.NoError(t, err)
			assert.Equal(t, "2", storedCheckpoint)
		})
	}
}

func TestPipeline_Run_RawBatchStreamConsumer_PrefersSessionCheckpointOverLegacy(t *testing.T) {
	testCases := []struct {
		name    string
		chain   model.Chain
		network model.Network
	}{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			checkpointStream := newCheckpointInMemoryStream()

			cfg := Config{
				Chain:                  tc.chain,
				Network:                tc.network,
				FetchWorkers:           1,
				NormalizerWorkers:      1,
				ChannelBufferSize:      2,
				StreamBackend:          checkpointStream,
				StreamNamespace:        "pipeline",
				StreamSessionID:        "session-a",
				StreamTransportEnabled: true,
			}
			p := New(cfg, nil, nil, &Repos{}, slog.Default())

			streamName := p.streamBoundaryName(streamBoundaryFetchToNormal)
			rawBatches := []event.RawBatch{
				{Chain: tc.chain, Network: tc.network, Address: "A"},
				{Chain: tc.chain, Network: tc.network, Address: "B"},
			}
			for _, batch := range rawBatches {
				_, err := checkpointStream.PublishJSON(context.Background(), streamName, batch)
				require.NoError(t, err)
			}

			sessionCheckpointKey := p.streamBoundaryCheckpointKey(streamBoundaryFetchToNormal)
			legacyCheckpointKey := p.streamBoundaryLegacyCheckpointKey(streamBoundaryFetchToNormal)
			require.NoError(t, checkpointStream.PersistStreamCheckpoint(context.Background(), sessionCheckpointKey, "1"))
			require.NoError(t, checkpointStream.PersistStreamCheckpoint(context.Background(), legacyCheckpointKey, "0"))

			loaded, err := p.loadStreamCheckpoint(context.Background(), checkpointStream, streamBoundaryFetchToNormal)
			require.NoError(t, err)
			assert.Equal(t, "1", loaded)

			output := collectRawBatchesThroughStreamBoundary(t, p, checkpointStream, 1)
			assert.Equal(t, rawBatches[1:], output)
		})
	}
}

func TestPipeline_Run_RawBatchStreamConsumer_DoesNotReuseSessionAgnosticCheckpointAcrossSessions(t *testing.T) {
	testCases := []struct {
		name    string
		chain   model.Chain
		network model.Network
	}{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
		},
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			checkpointStream := newCheckpointInMemoryStream()

			streamSessionA := Config{
				Chain:                  tc.chain,
				Network:                tc.network,
				FetchWorkers:           1,
				NormalizerWorkers:      1,
				ChannelBufferSize:      2,
				StreamBackend:          checkpointStream,
				StreamNamespace:        "pipeline",
				StreamSessionID:        "session-a",
				StreamTransportEnabled: true,
			}
			streamSessionB := streamSessionA
			streamSessionB.StreamSessionID = "session-b"

			pA := New(streamSessionA, nil, nil, &Repos{}, slog.Default())
			streamName := pA.streamBoundaryName(streamBoundaryFetchToNormal)
			rawBatches := []event.RawBatch{
				{Chain: tc.chain, Network: tc.network, Address: "A"},
				{Chain: tc.chain, Network: tc.network, Address: "B"},
			}
			_, err := checkpointStream.PublishJSON(context.Background(), streamName, rawBatches[0])
			require.NoError(t, err)
			_, err = checkpointStream.PublishJSON(context.Background(), streamName, rawBatches[1])
			require.NoError(t, err)

			gotA := collectRawBatchesThroughStreamBoundary(t, pA, checkpointStream, 2)
			assert.Equal(t, rawBatches, gotA)

			cpAKey := pA.streamBoundaryCheckpointKey(streamBoundaryFetchToNormal)
			storedCheckpointA, err := checkpointStream.LoadStreamCheckpoint(context.Background(), cpAKey)
			require.NoError(t, err)
			assert.Equal(t, "2", storedCheckpointA)

			pB := New(streamSessionB, nil, nil, &Repos{}, slog.Default())
			loadedFromB, err := pB.loadStreamCheckpoint(context.Background(), checkpointStream, streamBoundaryFetchToNormal)
			require.NoError(t, err)
			assert.Equal(t, "0", loadedFromB)

			gotB := collectRawBatchesThroughStreamBoundary(t, pB, checkpointStream, 2)
			assert.Equal(t, rawBatches, gotB)
		})
	}
}

type checkpointInMemoryStream struct {
	*redispkg.InMemoryStream
	checkpoints map[string]string
	mu          sync.Mutex
}

func newCheckpointInMemoryStream() *checkpointInMemoryStream {
	return &checkpointInMemoryStream{
		InMemoryStream: redispkg.NewInMemoryStream(),
		checkpoints:    map[string]string{},
	}
}

func (s *checkpointInMemoryStream) LoadStreamCheckpoint(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkpoints[key], nil
}

func (s *checkpointInMemoryStream) PersistStreamCheckpoint(_ context.Context, key string, streamID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkpoints[key] = streamID
	return nil
}

func collectRawBatchesThroughStreamBoundary(t *testing.T, p *Pipeline, stream redispkg.MessageTransport, expectedCount int) []event.RawBatch {
	t.Helper()

	streamName := p.streamBoundaryName(streamBoundaryFetchToNormal)
	normalizerInCh := make(chan event.RawBatch, expectedCount)

	runErr := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		runErr <- p.runRawBatchStreamConsumer(ctx, normalizerInCh, stream, streamName)
	}()

	got := make([]event.RawBatch, 0, expectedCount)
	for i := 0; i < expectedCount; i++ {
		got = append(got, <-normalizerInCh)
	}
	cancel()

	err := <-runErr
	assert.True(t, errors.Is(err, context.Canceled))

	return got
}
