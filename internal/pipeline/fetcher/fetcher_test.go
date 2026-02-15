package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	chainmocks "github.com/emperorhan/multichain-indexer/internal/chain/mocks"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/retry"
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
		FetchNewSignatures(gomock.Any(), "addr1", &cursor, 101).
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
		adapter:          mockAdapter,
		logger:           slog.Default(),
		retryMaxAttempts: 1,
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
		adapter:          mockAdapter,
		logger:           slog.Default(),
		retryMaxAttempts: 1,
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

func TestFetcher_Worker_PanicsOnProcessJobError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

	jobCh := make(chan event.FetchJob, 1)
	rawBatchCh := make(chan event.RawBatch, 1)
	f := &Fetcher{
		adapter:          mockAdapter,
		jobCh:            jobCh,
		rawBatchCh:       rawBatchCh,
		logger:           slog.Default(),
		retryMaxAttempts: 1,
	}

	jobCh <- event.FetchJob{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		Address:   "addr1",
		BatchSize: 100,
	}
	close(jobCh)

	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), "addr1", (*string)(nil), 100).
		Return(nil, errors.New("rpc timeout"))

	require.Panics(t, func() {
		f.worker(context.Background(), 0)
	})
}

func TestProcessJob_RetryBackoffAndAdaptiveReduction(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

	rawBatchCh := make(chan event.RawBatch, 1)
	var delays []time.Duration
	f := &Fetcher{
		adapter:          mockAdapter,
		rawBatchCh:       rawBatchCh,
		logger:           slog.Default(),
		retryMaxAttempts: 3,
		backoffInitial:   10 * time.Millisecond,
		backoffMax:       40 * time.Millisecond,
		adaptiveMinBatch: 1,
		sleepFn: func(_ context.Context, d time.Duration) error {
			delays = append(delays, d)
			return nil
		},
	}

	job := event.FetchJob{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		Address:   "addr1",
		BatchSize: 8,
	}

	gomock.InOrder(
		mockAdapter.EXPECT().
			FetchNewSignatures(gomock.Any(), "addr1", (*string)(nil), 8).
			Return(nil, errors.New("rpc timeout")),
		mockAdapter.EXPECT().
			FetchNewSignatures(gomock.Any(), "addr1", (*string)(nil), 4).
			Return(nil, errors.New("rpc timeout")),
		mockAdapter.EXPECT().
			FetchNewSignatures(gomock.Any(), "addr1", (*string)(nil), 2).
			Return([]chain.SignatureInfo{
				{Hash: "sig1", Sequence: 1},
				{Hash: "sig2", Sequence: 2},
			}, nil),
		mockAdapter.EXPECT().
			FetchTransactions(gomock.Any(), []string{"sig1", "sig2"}).
			Return([]json.RawMessage{
				json.RawMessage(`{"tx":1}`),
				json.RawMessage(`{"tx":2}`),
			}, nil),
	)

	err := f.processJob(context.Background(), slog.Default(), job)
	require.NoError(t, err)

	batch := <-rawBatchCh
	require.NotNil(t, batch.NewCursorValue)
	assert.Equal(t, "sig2", *batch.NewCursorValue)
	assert.Equal(t, int64(2), batch.NewCursorSequence)
	assert.Len(t, batch.RawTransactions, 2)
	assert.Equal(t, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}, delays)
}

func TestProcessJob_AdaptiveBatchStateAcrossRuns(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

	rawBatchCh := make(chan event.RawBatch, 1)
	var delays []time.Duration
	f := &Fetcher{
		adapter:          mockAdapter,
		rawBatchCh:       rawBatchCh,
		logger:           slog.Default(),
		retryMaxAttempts: 3,
		backoffInitial:   5 * time.Millisecond,
		backoffMax:       20 * time.Millisecond,
		adaptiveMinBatch: 1,
		sleepFn: func(_ context.Context, d time.Duration) error {
			delays = append(delays, d)
			return nil
		},
	}

	job := event.FetchJob{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		Address:   "addr1",
		BatchSize: 8,
	}

	gomock.InOrder(
		mockAdapter.EXPECT().
			FetchNewSignatures(gomock.Any(), "addr1", (*string)(nil), 8).
			Return([]chain.SignatureInfo{
				{Hash: "sig1", Sequence: 1},
				{Hash: "sig2", Sequence: 2},
				{Hash: "sig3", Sequence: 3},
				{Hash: "sig4", Sequence: 4},
			}, nil),
		mockAdapter.EXPECT().
			FetchTransactions(gomock.Any(), []string{"sig1", "sig2", "sig3", "sig4"}).
			Return(nil, errors.New("payload too large")),
		mockAdapter.EXPECT().
			FetchTransactions(gomock.Any(), []string{"sig1", "sig2"}).
			Return([]json.RawMessage{
				json.RawMessage(`{"tx":1}`),
				json.RawMessage(`{"tx":2}`),
			}, nil),
		// Next run should start from adapted size 2.
		mockAdapter.EXPECT().
			FetchNewSignatures(gomock.Any(), "addr1", (*string)(nil), 2).
			Return([]chain.SignatureInfo{}, nil),
	)

	err := f.processJob(context.Background(), slog.Default(), job)
	require.NoError(t, err)
	batch := <-rawBatchCh
	assert.Len(t, batch.RawTransactions, 2)
	require.NotNil(t, batch.NewCursorValue)
	assert.Equal(t, "sig2", *batch.NewCursorValue)

	err = f.processJob(context.Background(), slog.Default(), job)
	require.NoError(t, err)
	assert.Equal(t, []time.Duration{5 * time.Millisecond}, delays)
}

func TestFetcher_RetryDelay_ExponentialWithCap(t *testing.T) {
	f := &Fetcher{
		backoffInitial: 10 * time.Millisecond,
		backoffMax:     25 * time.Millisecond,
	}

	assert.Equal(t, 10*time.Millisecond, f.retryDelay(1))
	assert.Equal(t, 20*time.Millisecond, f.retryDelay(2))
	assert.Equal(t, 25*time.Millisecond, f.retryDelay(3))
	assert.Equal(t, 25*time.Millisecond, f.retryDelay(4))
}

func TestProcessJob_TerminalSignatureFetchError_NoRetryAcrossMandatoryChains(t *testing.T) {
	testCases := []struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
	}{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "addr1",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
			rawBatchCh := make(chan event.RawBatch, 1)

			sleepCalls := 0
			f := &Fetcher{
				adapter:          mockAdapter,
				rawBatchCh:       rawBatchCh,
				logger:           slog.Default(),
				retryMaxAttempts: 3,
				backoffInitial:   time.Millisecond,
				backoffMax:       2 * time.Millisecond,
				sleepFn: func(context.Context, time.Duration) error {
					sleepCalls++
					return nil
				},
			}

			job := event.FetchJob{
				Chain:     tc.chain,
				Network:   tc.network,
				Address:   tc.address,
				BatchSize: 4,
			}

			attempts := 0
			mockAdapter.EXPECT().
				FetchNewSignatures(gomock.Any(), tc.address, (*string)(nil), 4).
				DoAndReturn(func(context.Context, string, *string, int) ([]chain.SignatureInfo, error) {
					attempts++
					return nil, retry.Terminal(errors.New("invalid cursor"))
				}).
				Times(1)

			err := f.processJob(context.Background(), slog.Default(), job)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "terminal_failure stage=fetcher.fetch_signatures")
			assert.Equal(t, 1, attempts)
			assert.Equal(t, 0, sleepCalls)
			select {
			case got := <-rawBatchCh:
				t.Fatalf("expected no raw batch, got %+v", got)
			default:
			}
		})
	}
}

func TestProcessJob_TransientSignatureFetchRetryAcrossMandatoryChains(t *testing.T) {
	testCases := []struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
		txHash  string
		cursor  int64
	}{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "addr1",
			txHash:  "sig-sol-1",
			cursor:  101,
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			txHash:  "0xbase-sig-1",
			cursor:  202,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
			rawBatchCh := make(chan event.RawBatch, 1)

			var delays []time.Duration
			f := &Fetcher{
				adapter:          mockAdapter,
				rawBatchCh:       rawBatchCh,
				logger:           slog.Default(),
				retryMaxAttempts: 2,
				backoffInitial:   5 * time.Millisecond,
				backoffMax:       20 * time.Millisecond,
				adaptiveMinBatch: 1,
				sleepFn: func(_ context.Context, d time.Duration) error {
					delays = append(delays, d)
					return nil
				},
			}

			job := event.FetchJob{
				Chain:     tc.chain,
				Network:   tc.network,
				Address:   tc.address,
				BatchSize: 4,
			}

			gomock.InOrder(
				mockAdapter.EXPECT().
					FetchNewSignatures(gomock.Any(), tc.address, (*string)(nil), 4).
					Return(nil, retry.Transient(errors.New("rpc timeout"))),
				mockAdapter.EXPECT().
					FetchNewSignatures(gomock.Any(), tc.address, (*string)(nil), 2).
					Return([]chain.SignatureInfo{{Hash: tc.txHash, Sequence: tc.cursor}}, nil),
				mockAdapter.EXPECT().
					FetchTransactions(gomock.Any(), []string{tc.txHash}).
					Return([]json.RawMessage{json.RawMessage(`{"tx":1}`)}, nil),
			)

			require.NoError(t, f.processJob(context.Background(), slog.Default(), job))
			batch := <-rawBatchCh
			require.NotNil(t, batch.NewCursorValue)
			assert.Equal(t, tc.txHash, *batch.NewCursorValue)
			assert.Equal(t, tc.cursor, batch.NewCursorSequence)
			assert.Equal(t, []time.Duration{5 * time.Millisecond}, delays)
		})
	}
}

func TestProcessJob_TransientSignatureFetchExhaustion_StageDiagnostic(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

	f := &Fetcher{
		adapter:          mockAdapter,
		logger:           slog.Default(),
		retryMaxAttempts: 2,
		backoffInitial:   time.Millisecond,
		backoffMax:       2 * time.Millisecond,
		sleepFn:          func(context.Context, time.Duration) error { return nil },
	}

	job := event.FetchJob{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		Address:   "addr1",
		BatchSize: 4,
	}

	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), "addr1", (*string)(nil), 4).
		Return(nil, retry.Transient(errors.New("rpc timeout")))
	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), "addr1", (*string)(nil), 2).
		Return(nil, retry.Transient(errors.New("rpc timeout")))

	err := f.processJob(context.Background(), slog.Default(), job)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transient_recovery_exhausted stage=fetcher.fetch_signatures")
}

func TestProcessJob_BaseCanonicalizesOptionalPrefixCursorAlias(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

	f := &Fetcher{
		adapter:    mockAdapter,
		rawBatchCh: make(chan event.RawBatch, 1),
		logger:     slog.Default(),
	}

	cursor := "ABCDEF1234"
	job := event.FetchJob{
		Chain:       model.ChainBase,
		Network:     model.NetworkSepolia,
		Address:     "0x1111111111111111111111111111111111111111",
		CursorValue: &cursor,
		BatchSize:   16,
	}

	canonicalCursor := "0xabcdef1234"
	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), job.Address, &canonicalCursor, 17).
		Return([]chain.SignatureInfo{}, nil)

	require.NoError(t, f.processJob(context.Background(), slog.Default(), job))
}

func TestProcessJob_CursorBoundaryOverlapSuppressedAndProgressesAcrossMandatoryChains(t *testing.T) {
	testCases := []struct {
		name               string
		chain              model.Chain
		network            model.Network
		address            string
		cursor             string
		expectedCursor     string
		boundarySig        string
		newSig             string
		newSeq             int64
		expectedPrevCursor string
	}{
		{
			name:               "solana-devnet",
			chain:              model.ChainSolana,
			network:            model.NetworkDevnet,
			address:            "sol-boundary-addr",
			cursor:             "sol-boundary-1",
			expectedCursor:     "sol-boundary-2",
			boundarySig:        " sol-boundary-1 ",
			newSig:             "sol-boundary-2",
			newSeq:             102,
			expectedPrevCursor: "sol-boundary-1",
		},
		{
			name:               "base-sepolia",
			chain:              model.ChainBase,
			network:            model.NetworkSepolia,
			address:            "0x1111111111111111111111111111111111111111",
			cursor:             "ABCDEF",
			expectedCursor:     "0x123456",
			boundarySig:        "0xABCDEF",
			newSig:             "123456",
			newSeq:             202,
			expectedPrevCursor: "0xabcdef",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

			rawBatchCh := make(chan event.RawBatch, 1)
			f := &Fetcher{
				adapter:          mockAdapter,
				rawBatchCh:       rawBatchCh,
				logger:           slog.Default(),
				retryMaxAttempts: 1,
			}

			job := event.FetchJob{
				Chain:       tc.chain,
				Network:     tc.network,
				Address:     tc.address,
				CursorValue: &tc.cursor,
				BatchSize:   1,
			}

			mockAdapter.EXPECT().
				FetchNewSignatures(gomock.Any(), tc.address, gomock.Any(), 2).
				Return([]chain.SignatureInfo{
					{Hash: tc.boundarySig, Sequence: tc.newSeq - 1},
					{Hash: tc.newSig, Sequence: tc.newSeq},
				}, nil)

			mockAdapter.EXPECT().
				FetchTransactions(gomock.Any(), []string{tc.expectedCursor}).
				Return([]json.RawMessage{json.RawMessage(`{"tx":"boundary-new"}`)}, nil)

			require.NoError(t, f.processJob(context.Background(), slog.Default(), job))
			batch := <-rawBatchCh
			require.Len(t, batch.Signatures, 1)
			assert.Equal(t, tc.expectedCursor, batch.Signatures[0].Hash)
			assert.Equal(t, tc.newSeq, batch.Signatures[0].Sequence)
			require.NotNil(t, batch.PreviousCursorValue)
			assert.Equal(t, tc.expectedPrevCursor, *batch.PreviousCursorValue)
			require.NotNil(t, batch.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *batch.NewCursorValue)
			assert.Equal(t, tc.newSeq, batch.NewCursorSequence)
		})
	}
}

func TestProcessJob_BoundaryPartitionVarianceConvergesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name    string
		chain   model.Chain
		network model.Network
		address string
		rangeS  []chain.SignatureInfo
	}

	testCases := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "sol-partition-addr",
			rangeS: []chain.SignatureInfo{
				{Hash: "sol-partition-1", Sequence: 100},
				{Hash: "sol-partition-2", Sequence: 101},
				{Hash: "sol-partition-3", Sequence: 102},
			},
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			rangeS: []chain.SignatureInfo{
				{Hash: "AAA", Sequence: 200},
				{Hash: "0xBBB", Sequence: 201},
				{Hash: "ccc", Sequence: 202},
			},
		},
	}

	signatureHashes := func(sigs []event.SignatureInfo) []string {
		out := make([]string, 0, len(sigs))
		for _, sig := range sigs {
			out = append(out, sig.Hash)
		}
		return out
	}

	runStrategy := func(t *testing.T, tc testCase, jobs []event.FetchJob, sigResponses [][]chain.SignatureInfo) []string {
		t.Helper()

		ctrl := gomock.NewController(t)
		mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

		rawBatchCh := make(chan event.RawBatch, len(jobs))
		f := &Fetcher{
			adapter:          mockAdapter,
			rawBatchCh:       rawBatchCh,
			logger:           slog.Default(),
			retryMaxAttempts: 1,
		}

		for i, job := range jobs {
			fetchBatch := job.BatchSize
			if job.CursorValue != nil {
				fetchBatch++
			}

			expectedHashes := canonicalizeSignatures(tc.chain, sigResponses[i])
			expectedHashes = suppressBoundaryCursorSignatures(tc.chain, expectedHashes, canonicalizeCursorValue(tc.chain, job.CursorValue))
			if len(expectedHashes) > job.BatchSize {
				expectedHashes = expectedHashes[:job.BatchSize]
			}
			hashes := make([]string, 0, len(expectedHashes))
			for _, sig := range expectedHashes {
				hashes = append(hashes, sig.Hash)
			}

			mockAdapter.EXPECT().
				FetchNewSignatures(gomock.Any(), tc.address, gomock.Any(), fetchBatch).
				Return(sigResponses[i], nil)

			if len(hashes) == 0 {
				continue
			}

			mockAdapter.EXPECT().
				FetchTransactions(gomock.Any(), hashes).
				DoAndReturn(func(_ context.Context, got []string) ([]json.RawMessage, error) {
					payloads := make([]json.RawMessage, 0, len(got))
					for _, hash := range got {
						payloads = append(payloads, json.RawMessage(fmt.Sprintf(`{"tx":"%s"}`, hash)))
					}
					return payloads, nil
				})
		}

		collected := make([]string, 0, 4)
		for _, job := range jobs {
			f.setAdaptiveBatchSize(job.Address, job.BatchSize)
			require.NoError(t, f.processJob(context.Background(), slog.Default(), job))
			select {
			case batch := <-rawBatchCh:
				collected = append(collected, signatureHashes(batch.Signatures)...)
			default:
			}
		}
		return collected
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Strategy A: [s1,s2] then restart from boundary s2 -> [s2,s3]
			cursorA := tc.rangeS[1].Hash
			strategyA := runStrategy(t, tc,
				[]event.FetchJob{
					{Chain: tc.chain, Network: tc.network, Address: tc.address, BatchSize: 2},
					{Chain: tc.chain, Network: tc.network, Address: tc.address, CursorValue: &cursorA, BatchSize: 2},
				},
				[][]chain.SignatureInfo{
					{tc.rangeS[0], tc.rangeS[1]},
					{tc.rangeS[1], tc.rangeS[2]},
				},
			)

			// Strategy B: [s1] then restart from boundary s1 -> [s1,s2,s3]
			cursorB := tc.rangeS[0].Hash
			strategyB := runStrategy(t, tc,
				[]event.FetchJob{
					{Chain: tc.chain, Network: tc.network, Address: tc.address, BatchSize: 1},
					{Chain: tc.chain, Network: tc.network, Address: tc.address, CursorValue: &cursorB, BatchSize: 2},
				},
				[][]chain.SignatureInfo{
					{tc.rangeS[0]},
					{tc.rangeS[0], tc.rangeS[1], tc.rangeS[2]},
				},
			)

			expected := make([]string, 0, len(tc.rangeS))
			for _, sig := range canonicalizeSignatures(tc.chain, tc.rangeS) {
				expected = append(expected, sig.Hash)
			}
			assert.Equal(t, expected, strategyA)
			assert.Equal(t, expected, strategyB)
		})
	}
}

func TestProcessJob_CanonicalizesFetchOrderAndSuppressesOverlapDuplicatesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name           string
		chain          model.Chain
		network        model.Network
		address        string
		permutationA   []chain.SignatureInfo
		permutationB   []chain.SignatureInfo
		expectedHash   []string
		expectedSeqs   []int64
		expectedCursor string
		expectedSeq    int64
	}

	testCases := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "sol-addr-1",
			permutationA: []chain.SignatureInfo{
				{Hash: "sig3", Sequence: 300},
				{Hash: "sig1", Sequence: 100},
				{Hash: "sig2", Sequence: 200},
				{Hash: "sig2", Sequence: 200},
				{Hash: "sig3", Sequence: 300},
			},
			permutationB: []chain.SignatureInfo{
				{Hash: "sig2", Sequence: 200},
				{Hash: "sig3", Sequence: 300},
				{Hash: "sig1", Sequence: 100},
				{Hash: "sig3", Sequence: 300},
				{Hash: "sig2", Sequence: 200},
			},
			expectedHash:   []string{"sig1", "sig2", "sig3"},
			expectedSeqs:   []int64{100, 200, 300},
			expectedCursor: "sig3",
			expectedSeq:    300,
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			permutationA: []chain.SignatureInfo{
				{Hash: "CCC", Sequence: 30},
				{Hash: "AaA", Sequence: 10},
				{Hash: "bBb", Sequence: 20},
				{Hash: "0xBBB", Sequence: 20},
				{Hash: "0xaaa", Sequence: 10},
			},
			permutationB: []chain.SignatureInfo{
				{Hash: "0xBBB", Sequence: 20},
				{Hash: "ccc", Sequence: 30},
				{Hash: "AAA", Sequence: 10},
				{Hash: "0xbbb", Sequence: 20},
				{Hash: "aaa", Sequence: 10},
			},
			expectedHash:   []string{"0xaaa", "0xbbb", "0xccc"},
			expectedSeqs:   []int64{10, 20, 30},
			expectedCursor: "0xccc",
			expectedSeq:    30,
		},
	}

	signatureHashes := func(sigs []event.SignatureInfo) []string {
		out := make([]string, 0, len(sigs))
		for _, sig := range sigs {
			out = append(out, sig.Hash)
		}
		return out
	}

	signatureSequences := func(sigs []event.SignatureInfo) []int64 {
		out := make([]int64, 0, len(sigs))
		for _, sig := range sigs {
			out = append(out, sig.Sequence)
		}
		return out
	}

	run := func(t *testing.T, tc testCase, input []chain.SignatureInfo) event.RawBatch {
		t.Helper()

		ctrl := gomock.NewController(t)
		mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

		rawBatchCh := make(chan event.RawBatch, 1)
		f := &Fetcher{
			adapter:          mockAdapter,
			rawBatchCh:       rawBatchCh,
			logger:           slog.Default(),
			retryMaxAttempts: 1,
		}

		job := event.FetchJob{
			Chain:     tc.chain,
			Network:   tc.network,
			Address:   tc.address,
			BatchSize: 16,
		}

		mockAdapter.EXPECT().
			FetchNewSignatures(gomock.Any(), tc.address, (*string)(nil), 16).
			Return(input, nil)

		mockAdapter.EXPECT().
			FetchTransactions(gomock.Any(), tc.expectedHash).
			DoAndReturn(func(_ context.Context, hashes []string) ([]json.RawMessage, error) {
				payloads := make([]json.RawMessage, 0, len(hashes))
				for _, hash := range hashes {
					payloads = append(payloads, json.RawMessage(fmt.Sprintf(`{"tx":"%s"}`, hash)))
				}
				return payloads, nil
			})

		require.NoError(t, f.processJob(context.Background(), slog.Default(), job))
		return <-rawBatchCh
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			first := run(t, tc, tc.permutationA)
			second := run(t, tc, tc.permutationB)

			assert.Equal(t, tc.expectedHash, signatureHashes(first.Signatures))
			assert.Equal(t, tc.expectedHash, signatureHashes(second.Signatures))
			assert.Equal(t, tc.expectedSeqs, signatureSequences(first.Signatures))
			assert.Equal(t, signatureSequences(first.Signatures), signatureSequences(second.Signatures))

			require.NotNil(t, first.NewCursorValue)
			require.NotNil(t, second.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *first.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *second.NewCursorValue)
			assert.Equal(t, tc.expectedSeq, first.NewCursorSequence)
			assert.Equal(t, tc.expectedSeq, second.NewCursorSequence)
			assert.Equal(t, signatureHashes(first.Signatures), signatureHashes(second.Signatures))
		})
	}
}
