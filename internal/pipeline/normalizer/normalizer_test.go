package normalizer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/normalizer/mocks"
	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestProcessBatch_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	walletID := "wallet-1"
	cursorVal := "sig2"
	batch := event.RawBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		WalletID: &walletID,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":1}`),
			json.RawMessage(`{"tx":2}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "sig1", Sequence: 100},
			{Hash: "sig2", Sequence: 200},
		},
		NewCursorValue:    &cursorVal,
		NewCursorSequence: 200,
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...interface{}) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
			require.Len(t, req.Transactions, 2)
			assert.Equal(t, "sig1", req.Transactions[0].Signature)
			assert.Equal(t, "sig2", req.Transactions[1].Signature)
			assert.Equal(t, []string{"addr1"}, req.WatchedAddresses)

			return &sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					{
						TxHash:      "sig1",
						BlockCursor: 100,
						BlockTime:   1700000000,
						FeeAmount:   "5000",
						FeePayer:    "addr1",
						Status:      "SUCCESS",
						BalanceEvents: []*sidecarv1.BalanceEventInfo{
							{
								OuterInstructionIndex: 0,
								InnerInstructionIndex: -1,
								EventCategory:         "TRANSFER",
								EventAction:           "system_transfer",
								ProgramId:             "11111111111111111111111111111111",
								Address:               "addr1",
								ContractAddress:       "11111111111111111111111111111111",
								Delta:                 "-1000000",
								CounterpartyAddress:   "addr2",
								TokenSymbol:           "SOL",
								TokenName:             "Solana",
								TokenDecimals:         9,
								TokenType:             "NATIVE",
								Metadata:              map[string]string{},
							},
						},
					},
					{
						TxHash:      "sig2",
						BlockCursor: 200,
						BlockTime:   0, // no block time
						FeeAmount:   "5000",
						FeePayer:    "addr2",
						Status:      "SUCCESS",
					},
				},
			}, nil
		})

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)

	result := <-normalizedCh
	assert.Equal(t, model.ChainSolana, result.Chain)
	assert.Equal(t, model.NetworkDevnet, result.Network)
	assert.Equal(t, "addr1", result.Address)
	assert.Equal(t, &walletID, result.WalletID)
	assert.Equal(t, &cursorVal, result.NewCursorValue)
	assert.Equal(t, int64(200), result.NewCursorSequence)

	require.Len(t, result.Transactions, 2)

	// First tx
	tx1 := result.Transactions[0]
	assert.Equal(t, "sig1", tx1.TxHash)
	assert.Equal(t, int64(100), tx1.BlockCursor)
	require.NotNil(t, tx1.BlockTime)
	assert.Equal(t, int64(1700000000), tx1.BlockTime.Unix())
	assert.Equal(t, "5000", tx1.FeeAmount)
	assert.Equal(t, "addr1", tx1.FeePayer)
	assert.Equal(t, model.TxStatusSuccess, tx1.Status)
	require.Len(t, tx1.BalanceEvents, 1)

	be := tx1.BalanceEvents[0]
	assert.Equal(t, 0, be.OuterInstructionIndex)
	assert.Equal(t, -1, be.InnerInstructionIndex)
	assert.Equal(t, model.EventCategoryTransfer, be.EventCategory)
	assert.Equal(t, "system_transfer", be.EventAction)
	assert.Equal(t, "addr1", be.Address)
	assert.Equal(t, "addr2", be.CounterpartyAddress)
	assert.Equal(t, "-1000000", be.Delta)
	assert.Equal(t, model.TokenTypeNative, be.TokenType)

	// Second tx - no block time
	tx2 := result.Transactions[1]
	assert.Nil(t, tx2.BlockTime)
	assert.Empty(t, tx2.BalanceEvents)
}

func TestProcessBatch_DecodeErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000,
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	batch := event.RawBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":1}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "sig1", Sequence: 100},
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
			Results: []*sidecarv1.TransactionResult{},
			Errors: []*sidecarv1.DecodeError{
				{Signature: "sig1", Error: "parse error"},
			},
		}, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err) // decode errors are warnings, not failures

	result := <-normalizedCh
	assert.Empty(t, result.Transactions)
}

func TestProcessBatch_SidecarError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	n := &Normalizer{
		sidecarTimeout: 30_000_000_000,
		normalizedCh:   make(chan event.NormalizedBatch, 1),
		logger:         slog.Default(),
	}

	batch := event.RawBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":1}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "sig1", Sequence: 100},
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("sidecar unavailable"))

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sidecar unavailable")
}

func TestProcessBatch_ContextCanceled(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch) // unbuffered, blocks
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000,
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	batch := event.RawBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":1}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "sig1", Sequence: 100},
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
			Results: []*sidecarv1.TransactionResult{
				{TxHash: "sig1", BlockCursor: 100, Status: "SUCCESS"},
			},
		}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := n.processBatch(ctx, slog.Default(), mockClient, batch)
	require.Error(t, err)
}

func TestProcessBatch_ErrorField(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000,
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	batch := event.RawBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":1}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "sig1", Sequence: 100},
		},
	}

	errMsg := "instruction error"
	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
			Results: []*sidecarv1.TransactionResult{
				{
					TxHash:      "sig1",
					BlockCursor: 100,
					Status:      "FAILED",
					Error:       &errMsg,
				},
			},
		}, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)

	result := <-normalizedCh
	require.Len(t, result.Transactions, 1)
	assert.Equal(t, model.TxStatusFailed, result.Transactions[0].Status)
	require.NotNil(t, result.Transactions[0].Err)
	assert.Equal(t, "instruction error", *result.Transactions[0].Err)
}
