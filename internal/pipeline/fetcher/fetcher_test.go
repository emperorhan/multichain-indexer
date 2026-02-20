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
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
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

func TestFetcher_Worker_ReturnsErrorOnProcessJobFailure(t *testing.T) {
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

	err := f.worker(context.Background(), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetcher process job failed")
	assert.Contains(t, err.Error(), "rpc timeout")
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
		{
			name:    "btc-testnet",
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			address: "tb1fetcher-terminal",
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

func TestProcessJob_SeamCarryoverSuppressesPreCursorSequenceWhenNewerSignaturesExistAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name                string
		chain               model.Chain
		network             model.Network
		address             string
		cursor              string
		cursorSequence      int64
		signatures          []chain.SignatureInfo
		expectedHashes      []string
		expectedSequence    int64
		expectedCursorValue string
	}

	testCases := []testCase{
		{
			name:           "solana-devnet",
			chain:          model.ChainSolana,
			network:        model.NetworkDevnet,
			address:        "sol-seam-addr",
			cursor:         "sol-seam-boundary",
			cursorSequence: 100,
			signatures: []chain.SignatureInfo{
				{Hash: "sol-seam-stale", Sequence: 99},
				{Hash: " sol-seam-boundary ", Sequence: 100},
				{Hash: "sol-seam-same-sequence", Sequence: 100},
				{Hash: "sol-seam-new", Sequence: 101},
			},
			expectedHashes:      []string{"sol-seam-same-sequence", "sol-seam-new"},
			expectedSequence:    101,
			expectedCursorValue: "sol-seam-new",
		},
		{
			name:           "base-sepolia",
			chain:          model.ChainBase,
			network:        model.NetworkSepolia,
			address:        "0x1111111111111111111111111111111111111111",
			cursor:         "0xAA00",
			cursorSequence: 500,
			signatures: []chain.SignatureInfo{
				{Hash: "deadbeef", Sequence: 499},
				{Hash: "AA00", Sequence: 500},
				{Hash: "bb00", Sequence: 500},
				{Hash: "0xCC00", Sequence: 501},
			},
			expectedHashes:      []string{"0xbb00", "0xcc00"},
			expectedSequence:    501,
			expectedCursorValue: "0xcc00",
		},
	}

	signatureHashes := func(sigs []event.SignatureInfo) []string {
		out := make([]string, 0, len(sigs))
		for _, sig := range sigs {
			out = append(out, sig.Hash)
		}
		return out
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
				Chain:          tc.chain,
				Network:        tc.network,
				Address:        tc.address,
				CursorValue:    &tc.cursor,
				CursorSequence: tc.cursorSequence,
				BatchSize:      3,
			}

			mockAdapter.EXPECT().
				FetchNewSignatures(gomock.Any(), tc.address, gomock.Any(), 4).
				Return(tc.signatures, nil)

			mockAdapter.EXPECT().
				FetchTransactions(gomock.Any(), tc.expectedHashes).
				DoAndReturn(func(_ context.Context, hashes []string) ([]json.RawMessage, error) {
					raw := make([]json.RawMessage, 0, len(hashes))
					for _, hash := range hashes {
						raw = append(raw, json.RawMessage(fmt.Sprintf(`{"tx":"%s"}`, hash)))
					}
					return raw, nil
				})

			require.NoError(t, f.processJob(context.Background(), slog.Default(), job))
			batch := <-rawBatchCh
			assert.Equal(t, tc.expectedHashes, signatureHashes(batch.Signatures))
			assert.Equal(t, tc.expectedSequence, batch.NewCursorSequence)
			require.NotNil(t, batch.NewCursorValue)
			assert.Equal(t, tc.expectedCursorValue, *batch.NewCursorValue)
			assert.Equal(t, tc.cursorSequence, batch.PreviousCursorSequence)
		})
	}
}

func TestProcessJob_SeamCarryoverDeterministicPermutationsAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name                string
		chain               model.Chain
		network             model.Network
		address             string
		cursor              string
		cursorSequence      int64
		permutationA        []chain.SignatureInfo
		permutationB        []chain.SignatureInfo
		expectedHashes      []string
		expectedSequence    int64
		expectedCursorValue string
		batchSize           int
	}

	testCases := []testCase{
		{
			name:           "solana-devnet",
			chain:          model.ChainSolana,
			network:        model.NetworkDevnet,
			address:        "sol-carryover-addr",
			cursor:         "sol-boundary-100",
			cursorSequence: 100,
			permutationA: []chain.SignatureInfo{
				{Hash: "sol-carryover-new", Sequence: 102},
				{Hash: "sol-carryover-old", Sequence: 99},
				{Hash: " sol-carryover-boundary ", Sequence: 100},
				{Hash: "sol-carryover-same", Sequence: 100},
			},
			permutationB: []chain.SignatureInfo{
				{Hash: "sol-carryover-same", Sequence: 100},
				{Hash: "sol-carryover-old", Sequence: 99},
				{Hash: "sol-carryover-new", Sequence: 102},
				{Hash: " sol-carryover-boundary ", Sequence: 100},
			},
			expectedHashes:      []string{"sol-carryover-same", "sol-carryover-new"},
			expectedSequence:    102,
			expectedCursorValue: "sol-carryover-new",
		},
		{
			name:           "base-sepolia",
			chain:          model.ChainBase,
			network:        model.NetworkSepolia,
			address:        "0xbase-carryover-addr",
			cursor:         "0xBB00",
			cursorSequence: 200,
			permutationA: []chain.SignatureInfo{
				{Hash: "CC00", Sequence: 201},
				{Hash: " 0XBB00 ", Sequence: 200},
				{Hash: "bb00", Sequence: 200},
				{Hash: "AA00", Sequence: 199},
			},
			permutationB: []chain.SignatureInfo{
				{Hash: "AA00", Sequence: 199},
				{Hash: "bb00", Sequence: 200},
				{Hash: "CC00", Sequence: 201},
				{Hash: " 0XBB00 ", Sequence: 200},
			},
			expectedHashes:      []string{"0xcc00"},
			expectedSequence:    201,
			expectedCursorValue: "0xcc00",
		},
		{
			name:           "btc-testnet",
			chain:          model.ChainBTC,
			network:        model.NetworkTestnet,
			address:        "tb1c-addr",
			cursor:         "aabb",
			cursorSequence: 500,
			permutationA: []chain.SignatureInfo{
				{Hash: "tx-old", Sequence: 400},
				{Hash: " 0XbbAA", Sequence: 500},
				{Hash: "00aa", Sequence: 500},
				{Hash: "tx-new", Sequence: 501},
			},
			permutationB: []chain.SignatureInfo{
				{Hash: "00aa", Sequence: 500},
				{Hash: "tx-old", Sequence: 400},
				{Hash: "tx-new", Sequence: 501},
				{Hash: " 0XbbAA", Sequence: 500},
			},
			expectedHashes:      []string{"00aa", "bbaa", "tx-new"},
			expectedSequence:    501,
			expectedCursorValue: "tx-new",
			batchSize: 3,
		},
	}

	extractHashes := func(sigs []event.SignatureInfo) []string {
		out := make([]string, 0, len(sigs))
		for _, sig := range sigs {
			out = append(out, sig.Hash)
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

		cursor := tc.cursor
		batchSize := tc.batchSize
		if batchSize <= 0 {
			batchSize = 2
		}
		job := event.FetchJob{
			Chain:          tc.chain,
			Network:        tc.network,
			Address:        tc.address,
			CursorValue:    &cursor,
			CursorSequence: tc.cursorSequence,
			BatchSize:      batchSize,
		}

		mockAdapter.EXPECT().
			FetchNewSignatures(gomock.Any(), tc.address, gomock.Any(), batchSize+1).
			Return(input, nil)

		mockAdapter.EXPECT().
			FetchTransactions(gomock.Any(), tc.expectedHashes).
			DoAndReturn(func(_ context.Context, hashes []string) ([]json.RawMessage, error) {
				payloads := make([]json.RawMessage, 0, len(hashes))
				for _, hash := range hashes {
					payloads = append(payloads, json.RawMessage(fmt.Sprintf(`{"tx":"%s"}`, hash)))
				}
				return payloads, nil
			})

		require.NoError(t, f.processJob(context.Background(), slog.Default(), job))
		batch := <-rawBatchCh
		require.NotNil(t, batch.NewCursorValue)
		assert.Equal(t, tc.expectedCursorValue, *batch.NewCursorValue)
		require.Equal(t, tc.expectedSequence, batch.NewCursorSequence)
		return batch
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			first := run(t, tc, tc.permutationA)
			second := run(t, tc, tc.permutationB)

			assert.Equal(t, tc.expectedHashes, extractHashes(first.Signatures))
			assert.Equal(t, tc.expectedHashes, extractHashes(second.Signatures))
			assert.Equal(t, first.Signatures, second.Signatures)
		})
	}
}

func TestProcessJob_AdaptiveBatchCarryoverPreservesAcrossAliasRepresentativeSwitchAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name          string
		chain         model.Chain
		network       model.Network
		addressA      string
		addressB      string
		sigsA         []chain.SignatureInfo
		sigsB         []chain.SignatureInfo
		expectedA     []string
		expectedB     []string
		expectedCursor string
	}

	testCases := []testCase{
		{
			name:     "solana-devnet",
			chain:    model.ChainSolana,
			network:  model.NetworkDevnet,
			addressA: " sol-carryover-alias-addr ",
			addressB: "sol-carryover-alias-addr",
			sigsA: []chain.SignatureInfo{
				{Hash: "sol-alias-1", Sequence: 100},
				{Hash: "sol-alias-2", Sequence: 200},
			},
			sigsB: []chain.SignatureInfo{
				{Hash: "sol-alias-3", Sequence: 300},
				{Hash: "sol-alias-4", Sequence: 400},
			},
			expectedA:     []string{"sol-alias-1", "sol-alias-2"},
			expectedB:     []string{"sol-alias-3", "sol-alias-4"},
			expectedCursor: "sol-alias-4",
		},
		{
			name:     "base-sepolia",
			chain:    model.ChainBase,
			network:  model.NetworkSepolia,
			addressA: " 0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA ",
			addressB: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			sigsA: []chain.SignatureInfo{
				{Hash: "0xCC00", Sequence: 500},
				{Hash: "0xBB00", Sequence: 400},
			},
			sigsB: []chain.SignatureInfo{
				{Hash: "0xDD00", Sequence: 600},
				{Hash: "0xEE00", Sequence: 700},
			},
			expectedA:     []string{"0xbb00", "0xcc00"},
			expectedB:     []string{"0xdd00", "0xee00"},
			expectedCursor: "0xee00",
		},
		{
			name:     "btc-testnet",
			chain:    model.ChainBTC,
			network:  model.NetworkTestnet,
			addressA: " tb1qcarryoveraliasaddress000000000000000000 ",
			addressB: "tb1qcarryoveraliasaddress000000000000000000",
			sigsA: []chain.SignatureInfo{
				{Hash: "tb1-tx-1", Sequence: 120},
				{Hash: "tb1-tx-2", Sequence: 121},
			},
			sigsB: []chain.SignatureInfo{
				{Hash: "tb1-tx-3", Sequence: 130},
				{Hash: "tb1-tx-4", Sequence: 131},
			},
			expectedA:     []string{"tb1-tx-1", "tb1-tx-2"},
			expectedB:     []string{"tb1-tx-3", "tb1-tx-4"},
			expectedCursor: "tb1-tx-4",
		},
	}

	expectedTxs := func(hashes []string) []json.RawMessage {
		out := make([]json.RawMessage, 0, len(hashes))
		for _, hash := range hashes {
			out = append(out, json.RawMessage(fmt.Sprintf(`{"tx":"%s"}`, hash)))
		}
		return out
	}

	run := func(t *testing.T, tc testCase) event.RawBatch {
		t.Helper()
		ctrl := gomock.NewController(t)
		mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

		rawBatchCh := make(chan event.RawBatch, 2)
		f := &Fetcher{
			adapter:          mockAdapter,
			rawBatchCh:       rawBatchCh,
			logger:           slog.Default(),
			retryMaxAttempts: 1,
		}

		canonicalFetchAddress := canonicalizeWatchedAddressIdentity(tc.chain, tc.addressA)

		mockAdapter.EXPECT().
			FetchNewSignatures(gomock.Any(), canonicalFetchAddress, gomock.Any(), 4).
			Return(tc.sigsA, nil)
		mockAdapter.EXPECT().
			FetchTransactions(gomock.Any(), tc.expectedA).
			Return(expectedTxs(tc.expectedA), nil)

		mockAdapter.EXPECT().
			FetchNewSignatures(gomock.Any(), canonicalFetchAddress, gomock.Any(), 2).
			Return(tc.sigsB, nil)
		mockAdapter.EXPECT().
			FetchTransactions(gomock.Any(), tc.expectedB).
			Return(expectedTxs(tc.expectedB), nil)

		first := event.FetchJob{
			Chain:     tc.chain,
			Network:   tc.network,
			Address:   tc.addressA,
			BatchSize: 4,
		}
		second := event.FetchJob{
			Chain:     tc.chain,
			Network:   tc.network,
			Address:   tc.addressB,
			BatchSize: 4,
		}

		require.NoError(t, f.processJob(context.Background(), slog.Default(), first))
		firstBatch := <-rawBatchCh
		require.Len(t, firstBatch.Signatures, len(tc.expectedA))
		assert.Equal(t, tc.expectedA, []string{firstBatch.Signatures[0].Hash, firstBatch.Signatures[1].Hash})

		require.NoError(t, f.processJob(context.Background(), slog.Default(), second))
		secondBatch := <-rawBatchCh
		require.NotNil(t, secondBatch.NewCursorValue)
		assert.Equal(t, tc.expectedCursor, *secondBatch.NewCursorValue)
		return secondBatch
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			batch := run(t, tc)
			require.Len(t, batch.Signatures, len(tc.expectedB))
			assert.Equal(t, tc.expectedB, []string{batch.Signatures[0].Hash, batch.Signatures[1].Hash})
		})
	}
}

func TestProcessJob_AdaptiveBatchStateUsesChainNetworkScopeForAliasCanonicalizedAddresses(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)
	rawBatchCh := make(chan event.RawBatch, 2)
	f := &Fetcher{
		adapter:          mockAdapter,
		rawBatchCh:       rawBatchCh,
		logger:           slog.Default(),
		retryMaxAttempts: 1,
	}

	address := " shared-chain-network-addr "
	solAddress := canonicalizeWatchedAddressIdentity(model.ChainSolana, address)
	baseAddress := canonicalizeWatchedAddressIdentity(model.ChainBase, address)
	require.Equal(t, "shared-chain-network-addr", solAddress)
	require.Equal(t, "shared-chain-network-addr", baseAddress)

	solSignatures := []chain.SignatureInfo{
		{Hash: "sol-scoped-1", Sequence: 11},
		{Hash: "sol-scoped-2", Sequence: 12},
	}
	baseSignatures := []chain.SignatureInfo{
		{Hash: "base-scoped-1", Sequence: 21},
		{Hash: "base-scoped-2", Sequence: 22},
	}

	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), solAddress, gomock.Any(), 4).
		Return(solSignatures, nil)
	mockAdapter.EXPECT().
		FetchTransactions(gomock.Any(), []string{"sol-scoped-1", "sol-scoped-2"}).
		Return([]json.RawMessage{
			json.RawMessage(`{"tx":"sol-scoped-1"}`),
			json.RawMessage(`{"tx":"sol-scoped-2"}`),
		}, nil)

	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), baseAddress, gomock.Any(), 4).
		Return(baseSignatures, nil)
	mockAdapter.EXPECT().
		FetchTransactions(gomock.Any(), []string{"base-scoped-1", "base-scoped-2"}).
		Return([]json.RawMessage{
			json.RawMessage(`{"tx":"base-scoped-1"}`),
			json.RawMessage(`{"tx":"base-scoped-2"}`),
		}, nil)

	solanaJob := event.FetchJob{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		Address:   address,
		BatchSize: 4,
	}
	baseJob := event.FetchJob{
		Chain:     model.ChainBase,
		Network:   model.NetworkSepolia,
		Address:   address,
		BatchSize: 4,
	}

	require.NoError(t, f.processJob(context.Background(), slog.Default(), solanaJob))
	require.NoError(t, f.processJob(context.Background(), slog.Default(), baseJob))

	solBatch := <-rawBatchCh
	baseBatch := <-rawBatchCh
	require.Len(t, solBatch.Signatures, 2)
	require.Len(t, baseBatch.Signatures, 2)
	assert.Equal(t, []string{"sol-scoped-1", "sol-scoped-2"}, []string{solBatch.Signatures[0].Hash, solBatch.Signatures[1].Hash})
	assert.Equal(t, []string{"base-scoped-1", "base-scoped-2"}, []string{baseBatch.Signatures[0].Hash, baseBatch.Signatures[1].Hash})
}

func TestProcessJob_SeamCarryoverKeepsPreCursorSequenceWhenNoNewerSignatures(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockAdapter := chainmocks.NewMockChainAdapter(ctrl)

	rawBatchCh := make(chan event.RawBatch, 1)
	f := &Fetcher{
		adapter:          mockAdapter,
		rawBatchCh:       rawBatchCh,
		logger:           slog.Default(),
		retryMaxAttempts: 1,
	}

	cursor := "sol-reorg-boundary"
	job := event.FetchJob{
		Chain:          model.ChainSolana,
		Network:        model.NetworkDevnet,
		Address:        "sol-reorg-addr",
		CursorValue:    &cursor,
		CursorSequence: 100,
		BatchSize:      2,
	}

	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), job.Address, gomock.Any(), 3).
		Return([]chain.SignatureInfo{
			{Hash: "sol-reorg-older-1", Sequence: 98},
			{Hash: "sol-reorg-older-2", Sequence: 99},
		}, nil)

	mockAdapter.EXPECT().
		FetchTransactions(gomock.Any(), []string{"sol-reorg-older-1", "sol-reorg-older-2"}).
		Return([]json.RawMessage{
			json.RawMessage(`{"tx":"sol-reorg-older-1"}`),
			json.RawMessage(`{"tx":"sol-reorg-older-2"}`),
		}, nil)

	require.NoError(t, f.processJob(context.Background(), slog.Default(), job))
	batch := <-rawBatchCh
	require.Len(t, batch.Signatures, 2)
	assert.Equal(t, "sol-reorg-older-1", batch.Signatures[0].Hash)
	assert.Equal(t, int64(98), batch.Signatures[0].Sequence)
	assert.Equal(t, "sol-reorg-older-2", batch.Signatures[1].Hash)
	assert.Equal(t, int64(99), batch.Signatures[1].Sequence)
	require.NotNil(t, batch.NewCursorValue)
	assert.Equal(t, "sol-reorg-older-2", *batch.NewCursorValue)
	assert.Equal(t, int64(99), batch.NewCursorSequence)
	assert.Equal(t, int64(100), batch.PreviousCursorSequence)
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
			expectedHashes = suppressBoundaryCursorSignatures(tc.chain, expectedHashes, identity.CanonicalizeCursorValue(tc.chain, job.CursorValue), job.CursorSequence)
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
			f.setAdaptiveBatchSize(job.Chain, job.Network, job.Address, job.BatchSize)
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

type deterministicCutoffAdapter struct {
	chain      model.Chain
	signatures []chain.SignatureInfo // oldest-first fixture
}

func (a *deterministicCutoffAdapter) Chain() string {
	return a.chain.String()
}

func (a *deterministicCutoffAdapter) GetHeadSequence(context.Context) (int64, error) {
	if len(a.signatures) == 0 {
		return 0, nil
	}
	return a.signatures[len(a.signatures)-1].Sequence, nil
}

func (a *deterministicCutoffAdapter) FetchNewSignatures(
	ctx context.Context,
	address string,
	cursor *string,
	batchSize int,
) ([]chain.SignatureInfo, error) {
	return a.FetchNewSignaturesWithCutoff(ctx, address, cursor, batchSize, 0)
}

func (a *deterministicCutoffAdapter) FetchNewSignaturesWithCutoff(
	_ context.Context,
	_ string,
	cursor *string,
	batchSize int,
	cutoffSeq int64,
) ([]chain.SignatureInfo, error) {
	if batchSize <= 0 {
		return []chain.SignatureInfo{}, nil
	}

	filtered := make([]chain.SignatureInfo, 0, len(a.signatures))
	for _, sig := range a.signatures {
		if cutoffSeq > 0 && sig.Sequence > cutoffSeq {
			continue
		}
		filtered = append(filtered, sig)
	}

	start := 0
	if cursor != nil {
		cursorIdentity := identity.CanonicalSignatureIdentity(a.chain, *cursor)
		if cursorIdentity != "" {
			for idx, sig := range filtered {
				if identity.CanonicalSignatureIdentity(a.chain, sig.Hash) == cursorIdentity {
					// Include cursor overlap so fetcher boundary suppression is exercised.
					start = idx
					break
				}
			}
		}
	}

	if start >= len(filtered) {
		return []chain.SignatureInfo{}, nil
	}
	selected := append([]chain.SignatureInfo(nil), filtered[start:]...)
	if len(selected) > batchSize {
		selected = selected[:batchSize]
	}
	return selected, nil
}

func (a *deterministicCutoffAdapter) FetchTransactions(_ context.Context, hashes []string) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(hashes))
	for _, hash := range hashes {
		out = append(out, json.RawMessage(fmt.Sprintf(`{"tx":"%s"}`, hash)))
	}
	return out, nil
}

func TestProcessJob_PinnedCutoffPaginationPermutationsConvergeAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name           string
		chain          model.Chain
		network        model.Network
		address        string
		signatures     []chain.SignatureInfo
		cutoff         int64
		expectedHashes []string
		cursorA        string
		cursorB        string
	}

	testCases := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "sol-cutoff-addr",
			signatures: []chain.SignatureInfo{
				{Hash: "sol-cutoff-1", Sequence: 100},
				{Hash: "sol-cutoff-2", Sequence: 101},
				{Hash: "sol-cutoff-3", Sequence: 102},
				{Hash: "sol-cutoff-4", Sequence: 103},
				{Hash: "sol-cutoff-5", Sequence: 104}, // beyond pinned cutoff
			},
			cutoff:         103,
			expectedHashes: []string{"sol-cutoff-1", "sol-cutoff-2", "sol-cutoff-3", "sol-cutoff-4"},
			cursorA:        "sol-cutoff-2",
			cursorB:        "sol-cutoff-1",
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			signatures: []chain.SignatureInfo{
				{Hash: "AAA", Sequence: 200},
				{Hash: "0xBbB", Sequence: 201},
				{Hash: "ccc", Sequence: 202},
				{Hash: "0xDDD", Sequence: 203},
				{Hash: "eee", Sequence: 204}, // beyond pinned cutoff
			},
			cutoff:         203,
			expectedHashes: []string{"0xaaa", "0xbbb", "0xccc", "0xddd"},
			cursorA:        "0xBbB",
			cursorB:        "AAA",
		},
	}

	type jobSpec struct {
		cursor    *string
		batchSize int
		cutoff    int64
	}

	run := func(t *testing.T, tc testCase, specs []jobSpec) []string {
		t.Helper()

		rawBatchCh := make(chan event.RawBatch, len(specs))
		f := &Fetcher{
			adapter:          &deterministicCutoffAdapter{chain: tc.chain, signatures: tc.signatures},
			rawBatchCh:       rawBatchCh,
			logger:           slog.Default(),
			retryMaxAttempts: 1,
		}

		collected := make([]string, 0, len(tc.expectedHashes))
		for _, spec := range specs {
			job := event.FetchJob{
				Chain:          tc.chain,
				Network:        tc.network,
				Address:        tc.address,
				CursorValue:    spec.cursor,
				CursorSequence: signatureSequenceForCursor(tc.chain, tc.signatures, spec.cursor),
				FetchCutoffSeq: spec.cutoff,
				BatchSize:      spec.batchSize,
			}

			f.setAdaptiveBatchSize(tc.chain, tc.network, tc.address, spec.batchSize)
			require.NoError(t, f.processJob(context.Background(), slog.Default(), job))

			select {
			case batch := <-rawBatchCh:
				for _, sig := range batch.Signatures {
					assert.LessOrEqual(t, sig.Sequence, spec.cutoff)
					collected = append(collected, sig.Hash)
				}
			default:
			}
		}
		return collected
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cursorA := tc.cursorA
			cursorB := tc.cursorB
			strategyA := run(t, tc, []jobSpec{
				{batchSize: 2, cutoff: tc.cutoff},
				{cursor: &cursorA, batchSize: 2, cutoff: tc.cutoff},
			})
			strategyB := run(t, tc, []jobSpec{
				{batchSize: 1, cutoff: tc.cutoff},
				{cursor: &cursorB, batchSize: 3, cutoff: tc.cutoff},
			})

			assert.Equal(t, tc.expectedHashes, strategyA)
			assert.Equal(t, tc.expectedHashes, strategyB)
		})
	}
}

func TestProcessJob_PinnedCutoffLateAppendDeferredWithoutDuplicateAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name           string
		chain          model.Chain
		network        model.Network
		address        string
		signatures     []chain.SignatureInfo
		firstCutoff    int64
		secondCutoff   int64
		resumeCursor   string
		expectedHashes []string
	}

	testCases := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "sol-late-append-addr",
			signatures: []chain.SignatureInfo{
				{Hash: "sol-late-1", Sequence: 100},
				{Hash: "sol-late-2", Sequence: 101},
				{Hash: "sol-late-3", Sequence: 102},
				{Hash: "sol-late-4", Sequence: 103},
				{Hash: "sol-late-5", Sequence: 104},
			},
			firstCutoff:    102,
			secondCutoff:   104,
			resumeCursor:   "sol-late-3",
			expectedHashes: []string{"sol-late-1", "sol-late-2", "sol-late-3", "sol-late-4", "sol-late-5"},
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			signatures: []chain.SignatureInfo{
				{Hash: "AAA", Sequence: 200},
				{Hash: "bbb", Sequence: 201},
				{Hash: "CCC", Sequence: 202},
				{Hash: "ddd", Sequence: 203},
				{Hash: "0xEEE", Sequence: 204},
			},
			firstCutoff:    202,
			secondCutoff:   204,
			resumeCursor:   "CCC",
			expectedHashes: []string{"0xaaa", "0xbbb", "0xccc", "0xddd", "0xeee"},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rawBatchCh := make(chan event.RawBatch, 2)
			f := &Fetcher{
				adapter:          &deterministicCutoffAdapter{chain: tc.chain, signatures: tc.signatures},
				rawBatchCh:       rawBatchCh,
				logger:           slog.Default(),
				retryMaxAttempts: 1,
			}

			first := event.FetchJob{
				Chain:          tc.chain,
				Network:        tc.network,
				Address:        tc.address,
				BatchSize:      3,
				FetchCutoffSeq: tc.firstCutoff,
			}
			f.setAdaptiveBatchSize(tc.chain, tc.network, tc.address, first.BatchSize)
			require.NoError(t, f.processJob(context.Background(), slog.Default(), first))

			cursor := tc.resumeCursor
			second := event.FetchJob{
				Chain:          tc.chain,
				Network:        tc.network,
				Address:        tc.address,
				CursorValue:    &cursor,
				CursorSequence: signatureSequenceForCursor(tc.chain, tc.signatures, &cursor),
				BatchSize:      2,
				FetchCutoffSeq: tc.secondCutoff,
			}
			f.setAdaptiveBatchSize(tc.chain, tc.network, tc.address, second.BatchSize)
			require.NoError(t, f.processJob(context.Background(), slog.Default(), second))

			collected := make([]string, 0, len(tc.expectedHashes))
			for i := 0; i < 2; i++ {
				batch := <-rawBatchCh
				for _, sig := range batch.Signatures {
					assert.LessOrEqual(t, sig.Sequence, tc.secondCutoff)
					collected = append(collected, sig.Hash)
				}
			}

			assert.Equal(t, tc.expectedHashes, collected)
			seen := make(map[string]struct{}, len(collected))
			for _, hash := range collected {
				_, exists := seen[hash]
				assert.False(t, exists, "duplicate hash emitted across cutoff-boundary resume: %s", hash)
				seen[hash] = struct{}{}
			}
		})
	}
}

func TestProcessJob_PinnedCutoffReplayResumeConvergesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name           string
		chain          model.Chain
		network        model.Network
		address        string
		signatures     []chain.SignatureInfo
		cutoff         int64
		resumeCursor   string
		expectedHashes []string
	}

	testCases := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "sol-replay-cutoff-addr",
			signatures: []chain.SignatureInfo{
				{Hash: "sol-replay-1", Sequence: 100},
				{Hash: "sol-replay-2", Sequence: 101},
				{Hash: "sol-replay-3", Sequence: 102},
				{Hash: "sol-replay-4", Sequence: 103},
			},
			cutoff:         103,
			resumeCursor:   "sol-replay-2",
			expectedHashes: []string{"sol-replay-1", "sol-replay-2", "sol-replay-3", "sol-replay-4"},
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			signatures: []chain.SignatureInfo{
				{Hash: "AAA", Sequence: 200},
				{Hash: "bbb", Sequence: 201},
				{Hash: "CCC", Sequence: 202},
				{Hash: "ddd", Sequence: 203},
			},
			cutoff:         203,
			resumeCursor:   "bbb",
			expectedHashes: []string{"0xaaa", "0xbbb", "0xccc", "0xddd"},
		},
	}

	run := func(t *testing.T, tc testCase, jobs []event.FetchJob) ([]string, []int64) {
		t.Helper()
		rawBatchCh := make(chan event.RawBatch, len(jobs))
		f := &Fetcher{
			adapter:          &deterministicCutoffAdapter{chain: tc.chain, signatures: tc.signatures},
			rawBatchCh:       rawBatchCh,
			logger:           slog.Default(),
			retryMaxAttempts: 1,
		}

		out := make([]string, 0, len(tc.expectedHashes))
		cursorSeqs := make([]int64, 0, len(jobs))
		for _, job := range jobs {
			f.setAdaptiveBatchSize(tc.chain, tc.network, tc.address, job.BatchSize)
			require.NoError(t, f.processJob(context.Background(), slog.Default(), job))
			select {
			case batch := <-rawBatchCh:
				cursorSeqs = append(cursorSeqs, batch.NewCursorSequence)
				for _, sig := range batch.Signatures {
					out = append(out, sig.Hash)
				}
			default:
			}
		}
		return out, cursorSeqs
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			full, _ := run(t, tc, []event.FetchJob{
				{
					Chain:          tc.chain,
					Network:        tc.network,
					Address:        tc.address,
					BatchSize:      4,
					FetchCutoffSeq: tc.cutoff,
				},
			})

			cursor := tc.resumeCursor
			resume, resumeCursorSeqs := run(t, tc, []event.FetchJob{
				{
					Chain:          tc.chain,
					Network:        tc.network,
					Address:        tc.address,
					BatchSize:      2,
					FetchCutoffSeq: tc.cutoff,
				},
				{
					Chain:          tc.chain,
					Network:        tc.network,
					Address:        tc.address,
					CursorValue:    &cursor,
					CursorSequence: signatureSequenceForCursor(tc.chain, tc.signatures, &cursor),
					BatchSize:      2,
					FetchCutoffSeq: tc.cutoff,
				},
			})

			assert.Equal(t, tc.expectedHashes, full)
			assert.Equal(t, full, resume)
			for i := 1; i < len(resumeCursorSeqs); i++ {
				assert.GreaterOrEqual(t, resumeCursorSeqs[i], resumeCursorSeqs[i-1])
			}
		})
	}
}

func TestProcessJob_CutoffPostFilterDefersOutOfRangeSignaturesWhenAdapterIsNotCutoffAware(t *testing.T) {
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
		Chain:          model.ChainSolana,
		Network:        model.NetworkDevnet,
		Address:        "sol-cutoff-fallback",
		BatchSize:      4,
		FetchCutoffSeq: 102,
	}

	mockAdapter.EXPECT().
		FetchNewSignatures(gomock.Any(), job.Address, (*string)(nil), 4).
		Return([]chain.SignatureInfo{
			{Hash: "sig-100", Sequence: 100},
			{Hash: "sig-103", Sequence: 103},
			{Hash: "sig-102", Sequence: 102},
		}, nil)

	mockAdapter.EXPECT().
		FetchTransactions(gomock.Any(), []string{"sig-100", "sig-102"}).
		Return([]json.RawMessage{
			json.RawMessage(`{"tx":"sig-100"}`),
			json.RawMessage(`{"tx":"sig-102"}`),
		}, nil)

	require.NoError(t, f.processJob(context.Background(), slog.Default(), job))
	batch := <-rawBatchCh
	require.Len(t, batch.Signatures, 2)
	assert.Equal(t, "sig-100", batch.Signatures[0].Hash)
	assert.Equal(t, "sig-102", batch.Signatures[1].Hash)
	assert.Equal(t, int64(102), batch.NewCursorSequence)
}

func signatureSequenceForCursor(chainID model.Chain, sigs []chain.SignatureInfo, cursor *string) int64 {
	if cursor == nil {
		return 0
	}
	cursorIdentity := identity.CanonicalSignatureIdentity(chainID, *cursor)
	if cursorIdentity == "" {
		return 0
	}
	for _, sig := range sigs {
		if identity.CanonicalSignatureIdentity(chainID, sig.Hash) == cursorIdentity {
			return sig.Sequence
		}
	}
	return 0
}

func TestCanonicalSignatureIdentity_BTC(t *testing.T) {
	assert.Equal(t, "abcdef0011", identity.CanonicalSignatureIdentity(model.ChainBTC, "ABCDEF0011"))
	assert.Equal(t, "abcdef0011", identity.CanonicalSignatureIdentity(model.ChainBTC, "0xABCDEF0011"))
}

func TestCanonicalizeCursorValue_BTC(t *testing.T) {
	cursor := "ABCDEF0099"
	canonical := identity.CanonicalizeCursorValue(model.ChainBTC, &cursor)
	require.NotNil(t, canonical)
	assert.Equal(t, "abcdef0099", *canonical)
}
