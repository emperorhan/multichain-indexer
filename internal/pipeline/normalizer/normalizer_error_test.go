package normalizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/normalizer/mocks"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/retry"
	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// 1. gRPC decode failure (sidecar returns error)
// ---------------------------------------------------------------------------

func TestError_GRPCDecodeFailure_SidecarReturnsError(t *testing.T) {
	t.Run("sidecar_returns_generic_error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-err-grpc",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"grpc-err"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-grpc-err", Sequence: 100},
			},
		}

		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, errors.New("connection refused"))

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sidecar decode")
		assert.Contains(t, err.Error(), "connection refused")
		assert.Equal(t, 0, len(normalizedCh), "no normalized output should be emitted on gRPC failure")
	})

	t.Run("sidecar_returns_grpc_unavailable_status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-err-unavailable",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"grpc-unavail"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-grpc-unavail", Sequence: 200},
			},
		}

		grpcErr := status.Error(codes.Unavailable, "service unavailable")
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, grpcErr)

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sidecar decode")
		assert.Equal(t, 0, len(normalizedCh))
	})

	t.Run("sidecar_returns_grpc_internal_error_retry_exhausted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout:   30 * time.Second,
			normalizedCh:     normalizedCh,
			logger:           slog.Default(),
			retryMaxAttempts: 3,
			retryDelayStart:  0,
			retryDelayMax:    0,
			sleepFn:          func(context.Context, time.Duration) error { return nil },
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-err-internal",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"grpc-internal"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-grpc-internal", Sequence: 300},
			},
		}

		grpcErr := status.Error(codes.Internal, "internal server error")
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, grpcErr).
			Times(3)

		err := n.processBatchWithRetry(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "transient_recovery_exhausted")
		assert.Equal(t, 0, len(normalizedCh))
	})

	t.Run("sidecar_returns_terminal_grpc_error_no_retry", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		sleepCalls := 0
		n := &Normalizer{
			sidecarTimeout:   30 * time.Second,
			normalizedCh:     make(chan event.NormalizedBatch, 1),
			logger:           slog.Default(),
			retryMaxAttempts: 3,
			retryDelayStart:  time.Millisecond,
			retryDelayMax:    2 * time.Millisecond,
			sleepFn: func(context.Context, time.Duration) error {
				sleepCalls++
				return nil
			},
		}

		batch := event.RawBatch{
			Chain:           model.ChainSolana,
			Network:         model.NetworkDevnet,
			Address:         "addr-err-terminal",
			RawTransactions: []json.RawMessage{json.RawMessage(`{"tx":"terminal"}`)},
			Signatures:      []event.SignatureInfo{{Hash: "sig-terminal", Sequence: 400}},
		}

		terminalErr := retry.Terminal(errors.New("invalid argument from sidecar"))
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, terminalErr).
			Times(1)

		err := n.processBatchWithRetry(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "terminal_failure")
		assert.Equal(t, 0, sleepCalls, "terminal error must not trigger retry sleep")
	})

	t.Run("sidecar_returns_decode_errors_in_response_all_terminal", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-decode-err",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"decode-err-1"}`),
				json.RawMessage(`{"tx":"decode-err-2"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-decode-err-1", Sequence: 500},
				{Hash: "sig-decode-err-2", Sequence: 501},
			},
		}

		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{},
				Errors: []*sidecarv1.DecodeError{
					{Signature: "sig-decode-err-1", Error: "parse error"},
					{Signature: "sig-decode-err-2", Error: "schema mismatch"},
				},
			}, nil)

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode collapse")
		assert.Contains(t, err.Error(), "sig-decode-err-1=parse error")
		assert.Contains(t, err.Error(), "sig-decode-err-2=schema mismatch")
		assert.Equal(t, 0, len(normalizedCh))
	})
}

// ---------------------------------------------------------------------------
// 2. Empty batch handling
// ---------------------------------------------------------------------------

func TestError_EmptyBatchHandling(t *testing.T) {
	t.Run("empty_raw_transactions_and_signatures", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:           model.ChainSolana,
			Network:         model.NetworkDevnet,
			Address:         "addr-empty",
			RawTransactions: []json.RawMessage{},
			Signatures:      []event.SignatureInfo{},
		}

		// Empty sentinel batch: sidecar should NOT be called.
		// The normalizer passes it through directly so the ingester
		// can advance the watermark.

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.NoError(t, err)

		result := <-normalizedCh
		assert.Equal(t, model.ChainSolana, result.Chain)
		assert.Equal(t, "addr-empty", result.Address)
		assert.Empty(t, result.Transactions)
	})

	t.Run("nil_raw_transactions_and_nil_signatures", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:           model.ChainSolana,
			Network:         model.NetworkDevnet,
			Address:         "addr-nil",
			RawTransactions: nil,
			Signatures:      nil,
		}

		// Empty sentinel batch: sidecar should NOT be called.

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.NoError(t, err)

		result := <-normalizedCh
		assert.Empty(t, result.Transactions)
	})

	t.Run("sidecar_returns_nil_results", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-nil-results",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"nil-result"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-nil-result", Sequence: 100},
			},
		}

		// Return a response with nil Results slice and no errors.
		// The single signature has no matching result, triggering decode collapse.
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: nil,
			}, nil)

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode collapse")
		assert.Equal(t, 0, len(normalizedCh))
	})

	t.Run("sidecar_returns_results_with_nil_entries", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-nil-entries",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"nil-entry"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-nil-entry", Sequence: 100},
			},
		}

		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{nil, nil},
			}, nil)

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode collapse")
		assert.Equal(t, 0, len(normalizedCh))
	})
}

// ---------------------------------------------------------------------------
// 3. Missing watched address filtering
// ---------------------------------------------------------------------------

func TestError_MissingWatchedAddressFiltering(t *testing.T) {
	t.Run("results_for_different_address_are_not_matched", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "watched-addr-1",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"filter-1"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-filter-1", Sequence: 100},
			},
		}

		// Sidecar returns a result whose TxHash matches the signature
		// but with balance events for a completely different address.
		// This tests that the normalizer passes watched_addresses correctly
		// and processes whatever the sidecar returns.
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
				// Verify watched addresses are passed correctly to sidecar
				require.Equal(t, []string{"watched-addr-1"}, req.WatchedAddresses)

				return &sidecarv1.DecodeSolanaTransactionBatchResponse{
					Results: []*sidecarv1.TransactionResult{
						{
							TxHash:      "sig-filter-1",
							BlockCursor: 100,
							Status:      "SUCCESS",
							FeeAmount:   "5000",
							FeePayer:    "other-addr",
							BalanceEvents: []*sidecarv1.BalanceEventInfo{
								{
									OuterInstructionIndex: 0,
									InnerInstructionIndex: -1,
									EventCategory:         "TRANSFER",
									EventAction:           "system_transfer",
									ProgramId:             "11111111111111111111111111111111",
									Address:               "other-addr",
									ContractAddress:       "11111111111111111111111111111111",
									Delta:                 "-1000000",
									CounterpartyAddress:   "watched-addr-1",
									TokenSymbol:           "SOL",
									TokenName:             "Solana",
									TokenDecimals:         9,
									TokenType:             "NATIVE",
								},
							},
						},
					},
				}, nil
			})

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.NoError(t, err)

		result := <-normalizedCh
		require.Len(t, result.Transactions, 1)
		// Balance events are built by buildCanonicalSolanaBalanceEvents
		// which filters by watched address. Since the balance event address
		// is "other-addr" (not the watched "watched-addr-1"), only the
		// counterparty deposit event (if any) would be included.
		// The fee event is also excluded because feePayer != watchedAddress.
		// We verify the result is present and properly structured.
		assert.Equal(t, "sig-filter-1", result.Transactions[0].TxHash)
	})

	t.Run("watched_addresses_sent_to_sidecar_matches_batch_address", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		watchedAddr := "my-watched-wallet-abc"
		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: watchedAddr,
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"verify-watched"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-verify-watched", Sequence: 200},
			},
		}

		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
				require.Len(t, req.WatchedAddresses, 1)
				assert.Equal(t, watchedAddr, req.WatchedAddresses[0])

				return &sidecarv1.DecodeSolanaTransactionBatchResponse{
					Results: []*sidecarv1.TransactionResult{
						{
							TxHash:      "sig-verify-watched",
							BlockCursor: 200,
							Status:      "SUCCESS",
						},
					},
				}, nil
			})

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.NoError(t, err)

		result := <-normalizedCh
		require.Len(t, result.Transactions, 1)
	})

	t.Run("no_balance_events_when_feepayer_differs_from_watched_address", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "watched-fee-check",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"fee-check"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-fee-check", Sequence: 300},
			},
		}

		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					{
						TxHash:      "sig-fee-check",
						BlockCursor: 300,
						Status:      "SUCCESS",
						FeeAmount:   "5000",
						FeePayer:    "different-payer",
						// No balance events for the watched address
					},
				},
			}, nil)

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.NoError(t, err)

		result := <-normalizedCh
		require.Len(t, result.Transactions, 1)
		// No fee event emitted because feePayer != watched address
		assert.Empty(t, result.Transactions[0].BalanceEvents)
	})
}

// ---------------------------------------------------------------------------
// 4. Invalid transaction data handling
// ---------------------------------------------------------------------------

func TestError_InvalidTransactionDataHandling(t *testing.T) {
	t.Run("raw_signature_length_mismatch_returns_error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-mismatch",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":1}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig1", Sequence: 100},
				{Hash: "sig2", Sequence: 101},
			},
		}

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "length mismatch")
		assert.Contains(t, err.Error(), "raw=1")
		assert.Contains(t, err.Error(), "signatures=2")
	})

	t.Run("empty_signature_hashes_are_skipped", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-empty-sig",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"empty-sig-1"}`),
				json.RawMessage(`{"tx":"empty-sig-2"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "", Sequence: 100},
				{Hash: "   ", Sequence: 101},
			},
		}

		// All signatures are empty/whitespace-only: canonicalizeBatchSignatures
		// returns empty, but the input had signatures, so this triggers a
		// "decode collapse" error.
		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode collapse")
	})

	t.Run("whitespace_only_signature_trimmed_and_skipped", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		// Mix of valid and empty signatures
		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-mixed-sig",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"mixed-1"}`),
				json.RawMessage(`{"tx":"mixed-2"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "valid-sig", Sequence: 100},
				{Hash: "   ", Sequence: 101},
			},
		}

		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					{
						TxHash:      "valid-sig",
						BlockCursor: 100,
						Status:      "SUCCESS",
						FeeAmount:   "5000",
						FeePayer:    "addr-mixed-sig",
					},
				},
			}, nil)

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.NoError(t, err)

		result := <-normalizedCh
		require.Len(t, result.Transactions, 1)
		assert.Equal(t, "valid-sig", result.Transactions[0].TxHash)
	})

	t.Run("response_signature_does_not_match_request_signature", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-unmatched",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"unmatched"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "expected-sig", Sequence: 100},
			},
		}

		// Sidecar returns a completely different tx hash
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					{
						TxHash:      "totally-different-sig",
						BlockCursor: 100,
						Status:      "SUCCESS",
					},
				},
			}, nil)

		// With a single signature and a single unexpected result, the
		// single-signature fallback should apply. The normalizer recovers
		// via the fallback mechanism.
		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.NoError(t, err)

		result := <-normalizedCh
		require.Len(t, result.Transactions, 1)
	})

	t.Run("multiple_unmatched_signatures_causes_decode_collapse", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-collapse",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"collapse-1"}`),
				json.RawMessage(`{"tx":"collapse-2"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "expected-sig-1", Sequence: 100},
				{Hash: "expected-sig-2", Sequence: 101},
			},
		}

		// Sidecar returns results with completely wrong hashes
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					{TxHash: "wrong-sig-a", BlockCursor: 100, Status: "SUCCESS"},
					{TxHash: "wrong-sig-b", BlockCursor: 101, Status: "SUCCESS"},
				},
			}, nil)

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode collapse")
		assert.Equal(t, 0, len(normalizedCh))
	})

	t.Run("transaction_with_failed_status_is_preserved", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-failed-tx",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"failed"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-failed", Sequence: 100},
			},
		}

		errMsg := "custom instruction error"
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					{
						TxHash:      "sig-failed",
						BlockCursor: 100,
						Status:      "FAILED",
						Error:       &errMsg,
						FeeAmount:   "5000",
						FeePayer:    "addr-failed-tx",
					},
				},
			}, nil)

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.NoError(t, err)

		result := <-normalizedCh
		require.Len(t, result.Transactions, 1)
		assert.Equal(t, model.TxStatusFailed, result.Transactions[0].Status)
		require.NotNil(t, result.Transactions[0].Err)
		assert.Equal(t, "custom instruction error", *result.Transactions[0].Err)
	})
}

// ---------------------------------------------------------------------------
// 5. Context cancellation during normalization
// ---------------------------------------------------------------------------

func TestError_ContextCancellationDuringNormalization(t *testing.T) {
	t.Run("context_cancelled_before_grpc_call", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-ctx-cancel",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"ctx-cancel"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-ctx-cancel", Sequence: 100},
			},
		}

		// The sidecar returns context.Canceled because the ctx has been cancelled.
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, context.Canceled)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		err := n.processBatch(ctx, slog.Default(), mockClient, batch)
		require.Error(t, err)
	})

	t.Run("context_cancelled_during_channel_send", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		// Unbuffered channel: will block on send
		normalizedCh := make(chan event.NormalizedBatch)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-ctx-send",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"ctx-send"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-ctx-send", Sequence: 100},
			},
		}

		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					{TxHash: "sig-ctx-send", BlockCursor: 100, Status: "SUCCESS"},
				},
			}, nil)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel before processBatch can send to channel

		err := n.processBatch(ctx, slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("context_cancelled_during_retry_sleep", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		n := &Normalizer{
			sidecarTimeout:   30 * time.Second,
			normalizedCh:     make(chan event.NormalizedBatch, 1),
			logger:           slog.Default(),
			retryMaxAttempts: 3,
			retryDelayStart:  100 * time.Millisecond,
			retryDelayMax:    500 * time.Millisecond,
			sleepFn: func(ctx context.Context, d time.Duration) error {
				// Simulate context cancellation during sleep
				return context.Canceled
			},
		}

		batch := event.RawBatch{
			Chain:             model.ChainSolana,
			Network:           model.NetworkDevnet,
			Address:           "addr-ctx-sleep",
			RawTransactions:   []json.RawMessage{json.RawMessage(`{"tx":"ctx-sleep"}`)},
			Signatures:        []event.SignatureInfo{{Hash: "sig-ctx-sleep", Sequence: 100}},
		}

		// First attempt fails with a transient error, triggering a retry
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, errors.New("sidecar unavailable")).
			Times(1)

		err := n.processBatchWithRetry(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("worker_returns_context_error_on_cancellation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		rawBatchCh := make(chan event.RawBatch, 1)
		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			rawBatchCh:     rawBatchCh,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		// Cancel context before putting anything on the channel
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := n.worker(ctx, 0, mockClient)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("worker_returns_nil_when_channel_closed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		rawBatchCh := make(chan event.RawBatch)
		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			rawBatchCh:     rawBatchCh,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		close(rawBatchCh) // close immediately

		err := n.worker(context.Background(), 0, mockClient)
		require.NoError(t, err) // worker exits cleanly on closed channel
	})

	t.Run("context_deadline_exceeded_during_grpc_call", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout:   30 * time.Second,
			normalizedCh:     normalizedCh,
			logger:           slog.Default(),
			retryMaxAttempts: 2,
			retryDelayStart:  0,
			retryDelayMax:    0,
			sleepFn:          func(context.Context, time.Duration) error { return nil },
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr-deadline",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"deadline"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig-deadline", Sequence: 100},
			},
		}

		// Simulate deadline exceeded from gRPC
		grpcErr := status.Error(codes.DeadlineExceeded, "context deadline exceeded")
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, grpcErr).
			Times(2) // retried once, then exhausted

		err := n.processBatchWithRetry(context.Background(), slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "transient_recovery_exhausted")
		assert.Equal(t, 0, len(normalizedCh))
	})

	t.Run("processBatchWithRetry_returns_context_error_when_context_cancelled_mid_retry", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		n := &Normalizer{
			sidecarTimeout:   30 * time.Second,
			normalizedCh:     make(chan event.NormalizedBatch, 1),
			logger:           slog.Default(),
			retryMaxAttempts: 3,
			retryDelayStart:  0,
			retryDelayMax:    0,
			sleepFn:          func(context.Context, time.Duration) error { return nil },
		}

		batch := event.RawBatch{
			Chain:             model.ChainSolana,
			Network:           model.NetworkDevnet,
			Address:           "addr-ctx-mid-retry",
			RawTransactions:   []json.RawMessage{json.RawMessage(`{"tx":"mid-retry"}`)},
			Signatures:        []event.SignatureInfo{{Hash: "sig-mid-retry", Sequence: 100}},
		}

		ctx, cancel := context.WithCancel(context.Background())
		callCount := 0

		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
				callCount++
				if callCount == 1 {
					// First call fails transiently
					return nil, fmt.Errorf("sidecar unavailable")
				}
				// Cancel context before second attempt completes
				cancel()
				return nil, context.Canceled
			}).
			Times(2)

		err := n.processBatchWithRetry(ctx, slog.Default(), mockClient, batch)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})
}
