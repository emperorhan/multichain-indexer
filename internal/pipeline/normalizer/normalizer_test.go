package normalizer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
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
)

type baseFeeEventFixture struct {
	OuterInstructionIndex int32             `json:"outer_instruction_index"`
	InnerInstructionIndex int32             `json:"inner_instruction_index"`
	EventCategory         string            `json:"event_category"`
	EventAction           string            `json:"event_action"`
	ProgramID             string            `json:"program_id"`
	Address               string            `json:"address"`
	ContractAddress       string            `json:"contract_address"`
	Delta                 string            `json:"delta"`
	CounterpartyAddress   string            `json:"counterparty_address"`
	TokenSymbol           string            `json:"token_symbol"`
	TokenName             string            `json:"token_name"`
	TokenDecimals         int32             `json:"token_decimals"`
	TokenType             string            `json:"token_type"`
	Metadata              map[string]string `json:"metadata"`
}

type baseFeeFixture struct {
	TxHash        string                `json:"tx_hash"`
	BlockCursor   int64                 `json:"block_cursor"`
	BlockTime     int64                 `json:"block_time"`
	Status        string                `json:"status"`
	FeeAmount     string                `json:"fee_amount"`
	FeePayer      string                `json:"fee_payer"`
	BalanceEvents []baseFeeEventFixture `json:"balance_events"`
}

func loadBaseFeeFixture(t *testing.T, fixture string) *sidecarv1.TransactionResult {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", fixture))
	require.NoError(t, err)

	var parsed baseFeeFixture
	require.NoError(t, json.Unmarshal(raw, &parsed))

	events := make([]*sidecarv1.BalanceEventInfo, 0, len(parsed.BalanceEvents))
	for _, be := range parsed.BalanceEvents {
		beCopy := be
		events = append(events, &sidecarv1.BalanceEventInfo{
			OuterInstructionIndex: beCopy.OuterInstructionIndex,
			InnerInstructionIndex: beCopy.InnerInstructionIndex,
			EventCategory:         beCopy.EventCategory,
			EventAction:           beCopy.EventAction,
			ProgramId:             beCopy.ProgramID,
			Address:               beCopy.Address,
			ContractAddress:       beCopy.ContractAddress,
			Delta:                 beCopy.Delta,
			CounterpartyAddress:   beCopy.CounterpartyAddress,
			TokenSymbol:           beCopy.TokenSymbol,
			TokenName:             beCopy.TokenName,
			TokenDecimals:         beCopy.TokenDecimals,
			TokenType:             beCopy.TokenType,
			Metadata:              beCopy.Metadata,
		})
	}

	return &sidecarv1.TransactionResult{
		TxHash:        parsed.TxHash,
		BlockCursor:   parsed.BlockCursor,
		BlockTime:     parsed.BlockTime,
		FeeAmount:     parsed.FeeAmount,
		FeePayer:      parsed.FeePayer,
		Status:        parsed.Status,
		BalanceEvents: events,
	}
}

func loadSolanaFixture(t *testing.T, fixture string) *sidecarv1.TransactionResult {
	t.Helper()
	return loadBaseFeeFixture(t, fixture)
}

type tupleSignature struct {
	TxHash        string
	EventID       string
	EventCategory string
	Delta         string
}

func orderedCanonicalTuples(batch event.NormalizedBatch) []tupleSignature {
	tuples := make([]tupleSignature, 0)
	for _, tx := range batch.Transactions {
		for _, be := range tx.BalanceEvents {
			tuples = append(tuples, tupleSignature{
				TxHash:        tx.TxHash,
				EventID:       be.EventID,
				EventCategory: string(be.EventCategory),
				Delta:         be.Delta,
			})
		}
	}
	return tuples
}

func orderedCanonicalEventIDs(batches []event.NormalizedBatch) []string {
	ids := make([]string, 0)
	for _, batch := range batches {
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				ids = append(ids, be.EventID)
			}
		}
	}
	return ids
}

func fixtureBaseFeeEventsByCategory(events []event.NormalizedBalanceEvent, category model.EventCategory) []event.NormalizedBalanceEvent {
	out := make([]event.NormalizedBalanceEvent, 0, len(events))
	for _, be := range events {
		if be.EventCategory == category {
			out = append(out, be)
		}
	}
	return out
}

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
	prevCursor := "sig0"
	cursorVal := "sig2"
	batch := event.RawBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		Address:                "addr1",
		WalletID:               &walletID,
		PreviousCursorValue:    &prevCursor,
		PreviousCursorSequence: 50,
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
	assert.Equal(t, &prevCursor, result.PreviousCursorValue)
	assert.Equal(t, int64(50), result.PreviousCursorSequence)
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
	require.Len(t, tx1.BalanceEvents, 2)

	var feeEvent1, transferEvent1 event.NormalizedBalanceEvent
	foundFeeEvent := false
	foundTransferEvent := false
	for i := range tx1.BalanceEvents {
		be := tx1.BalanceEvents[i]
		if be.EventCategory == model.EventCategoryFee {
			feeEvent1 = be
			foundFeeEvent = true
		}
		if be.EventCategory == model.EventCategoryTransfer {
			transferEvent1 = be
			foundTransferEvent = true
		}
	}
	require.True(t, foundFeeEvent)
	assert.Equal(t, model.EventCategoryFee, feeEvent1.EventCategory)
	assert.Equal(t, "transaction_fee", feeEvent1.EventAction)
	assert.Equal(t, "addr1", feeEvent1.Address)
	assert.Equal(t, "-5000", feeEvent1.Delta)
	assert.Equal(t, "outer:-1|inner:-1", feeEvent1.EventPath)

	require.True(t, foundTransferEvent)
	assert.Equal(t, 0, transferEvent1.OuterInstructionIndex)
	assert.Equal(t, -1, transferEvent1.InnerInstructionIndex)
	assert.Equal(t, model.EventCategoryTransfer, transferEvent1.EventCategory)
	assert.Equal(t, "system_transfer", transferEvent1.EventAction)
	assert.NotEmpty(t, transferEvent1.EventID)
	assert.Equal(t, "addr1", transferEvent1.Address)
	assert.Equal(t, "addr2", transferEvent1.CounterpartyAddress)
	assert.Equal(t, "-1000000", transferEvent1.Delta)
	assert.Equal(t, model.TokenTypeNative, transferEvent1.TokenType)

	// Second tx - no block time
	tx2 := result.Transactions[1]
	assert.Nil(t, tx2.BlockTime)
	require.Len(t, tx2.BalanceEvents, 1)
	assert.Equal(t, model.EventCategoryFee, tx2.BalanceEvents[0].EventCategory)
	assert.Equal(t, "addr2", tx2.BalanceEvents[0].Address)
	assert.Equal(t, "-5000", tx2.BalanceEvents[0].Delta)
}

func TestProcessBatch_EventIDDeterminism(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 2)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
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

	response := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			{
				TxHash:      "sig1",
				BlockCursor: 100,
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
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Times(2).
		Return(response, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)
	first := <-normalizedCh

	err = n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)
	second := <-normalizedCh

	require.Len(t, first.Transactions, 1)
	require.Len(t, second.Transactions, 1)
	require.Len(t, first.Transactions[0].BalanceEvents, 1)
	require.Len(t, second.Transactions[0].BalanceEvents, 1)
	assert.Equal(t, first.Transactions[0].BalanceEvents[0].EventID, second.Transactions[0].BalanceEvents[0].EventID)
	assert.NotEmpty(t, first.Transactions[0].BalanceEvents[0].EventID)
}

func TestProcessBatch_BaseResultMatching_DeterministicAcrossResponseOrderAndHashCase(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 2)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000,
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	batch := event.RawBatch{
		Chain:   model.ChainBase,
		Network: model.NetworkSepolia,
		Address: "0x1111111111111111111111111111111111111111",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"base-order-1"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "0xABCDEF", Sequence: 77},
		},
	}

	matched := &sidecarv1.TransactionResult{
		TxHash:      "0xabcdef",
		BlockCursor: 77,
		Status:      "SUCCESS",
		BalanceEvents: []*sidecarv1.BalanceEventInfo{
			{
				OuterInstructionIndex: 0,
				InnerInstructionIndex: -1,
				EventCategory:         string(model.EventCategoryTransfer),
				EventAction:           "native_transfer",
				ProgramId:             "0xbase-program",
				Address:               "0x1111111111111111111111111111111111111111",
				ContractAddress:       "ETH",
				Delta:                 "-1",
				TokenSymbol:           "ETH",
				TokenName:             "Ether",
				TokenDecimals:         18,
				TokenType:             string(model.TokenTypeNative),
			},
		},
	}
	unrelated := &sidecarv1.TransactionResult{
		TxHash:      "0x0000000000000000000000000000000000000000000000000000000000000001",
		BlockCursor: 78,
		Status:      "SUCCESS",
	}

	call := 0
	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Times(2).
		DoAndReturn(func(_ context.Context, _ *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
			call++
			if call == 1 {
				return &sidecarv1.DecodeSolanaTransactionBatchResponse{
					Results: []*sidecarv1.TransactionResult{unrelated, matched},
				}, nil
			}
			return &sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{matched, unrelated},
			}, nil
		})

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
	first := <-normalizedCh
	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
	second := <-normalizedCh

	require.Len(t, first.Transactions, 1)
	require.Len(t, second.Transactions, 1)
	assert.Equal(t, "0xabcdef", first.Transactions[0].TxHash)
	assert.Equal(t, "0xabcdef", second.Transactions[0].TxHash)
	assert.Equal(t, orderedCanonicalTuples(first), orderedCanonicalTuples(second))
	require.NotNil(t, first.NewCursorValue)
	require.NotNil(t, second.NewCursorValue)
	assert.Equal(t, "0xabcdef", *first.NewCursorValue)
	assert.Equal(t, "0xabcdef", *second.NewCursorValue)
}

func TestProcessBatch_AliasIdentityConvergenceAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name           string
		chain          model.Chain
		network        model.Network
		address        string
		signatures     []event.SignatureInfo
		expectedTxs    []string
		expectedCursor string
		responseRunOne []*sidecarv1.TransactionResult
		responseRunTwo []*sidecarv1.TransactionResult
	}

	buildTransfer := func(txHash string, cursor int64, address string) *sidecarv1.TransactionResult {
		return &sidecarv1.TransactionResult{
			TxHash:      txHash,
			BlockCursor: cursor,
			Status:      "SUCCESS",
			FeeAmount:   "1",
			FeePayer:    address,
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					OuterInstructionIndex: 0,
					InnerInstructionIndex: -1,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "transfer",
					ProgramId:             "program",
					Address:               address,
					ContractAddress:       "ETH",
					Delta:                 "-1",
					TokenSymbol:           "ETH",
					TokenName:             "Ether",
					TokenDecimals:         18,
					TokenType:             string(model.TokenTypeNative),
				},
			},
		}
	}

	testCases := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "solana-addr-1",
			signatures: []event.SignatureInfo{
				{Hash: " sig-alias-1 ", Sequence: 10},
				{Hash: "sig-alias-2", Sequence: 11},
			},
			expectedTxs:    []string{"sig-alias-1", "sig-alias-2"},
			expectedCursor: "sig-alias-2",
			responseRunOne: []*sidecarv1.TransactionResult{
				buildTransfer("sig-alias-2", 11, "solana-addr-1"),
				buildTransfer("sig-alias-1", 10, "solana-addr-1"),
			},
			responseRunTwo: []*sidecarv1.TransactionResult{
				buildTransfer(" sig-alias-1 ", 10, "solana-addr-1"),
				buildTransfer("sig-alias-2", 11, "solana-addr-1"),
			},
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			signatures: []event.SignatureInfo{
				{Hash: "ABCDEF", Sequence: 20},
				{Hash: "0x1234", Sequence: 21},
			},
			expectedTxs:    []string{"0xabcdef", "0x1234"},
			expectedCursor: "0x1234",
			responseRunOne: []*sidecarv1.TransactionResult{
				buildTransfer("0xabcdef", 20, "0x1111111111111111111111111111111111111111"),
				buildTransfer("0x1234", 21, "0x1111111111111111111111111111111111111111"),
			},
			responseRunTwo: []*sidecarv1.TransactionResult{
				buildTransfer("ABCDEF", 20, "0x1111111111111111111111111111111111111111"),
				buildTransfer("1234", 21, "0x1111111111111111111111111111111111111111"),
			},
		},
	}

	txHashes := func(batch event.NormalizedBatch) []string {
		out := make([]string, 0, len(batch.Transactions))
		for _, tx := range batch.Transactions {
			out = append(out, tx.TxHash)
		}
		return out
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)
			normalizedCh := make(chan event.NormalizedBatch, 2)
			n := &Normalizer{
				sidecarTimeout: 30 * time.Second,
				normalizedCh:   normalizedCh,
				logger:         slog.Default(),
			}

			batch := event.RawBatch{
				Chain:   tc.chain,
				Network: tc.network,
				Address: tc.address,
				RawTransactions: []json.RawMessage{
					json.RawMessage(`{"tx":1}`),
					json.RawMessage(`{"tx":2}`),
				},
				Signatures: tc.signatures,
			}

			call := 0
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(2).
				DoAndReturn(func(context.Context, *sidecarv1.DecodeSolanaTransactionBatchRequest, ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					call++
					if call == 1 {
						return &sidecarv1.DecodeSolanaTransactionBatchResponse{Results: tc.responseRunOne}, nil
					}
					return &sidecarv1.DecodeSolanaTransactionBatchResponse{Results: tc.responseRunTwo}, nil
				})

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			first := <-normalizedCh
			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			second := <-normalizedCh

			assert.Equal(t, tc.expectedTxs, txHashes(first))
			assert.Equal(t, tc.expectedTxs, txHashes(second))
			assert.Equal(t, orderedCanonicalTuples(first), orderedCanonicalTuples(second))
			require.NotNil(t, first.NewCursorValue)
			require.NotNil(t, second.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *first.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *second.NewCursorValue)
		})
	}
}

func TestProcessBatch_AliasOverlapDuplicatesSuppressedAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name           string
		chain          model.Chain
		network        model.Network
		address        string
		signatures     []event.SignatureInfo
		expectedTxs    []string
		expectedCursor string
		responseA      []*sidecarv1.TransactionResult
		responseB      []*sidecarv1.TransactionResult
	}

	buildTransfer := func(txHash string, cursor int64, address string, contract string) *sidecarv1.TransactionResult {
		return &sidecarv1.TransactionResult{
			TxHash:      txHash,
			BlockCursor: cursor,
			Status:      "SUCCESS",
			FeeAmount:   "1",
			FeePayer:    address,
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					OuterInstructionIndex: 0,
					InnerInstructionIndex: -1,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "transfer",
					ProgramId:             "program",
					Address:               address,
					ContractAddress:       contract,
					Delta:                 "-1",
					TokenSymbol:           "ETH",
					TokenName:             "Ether",
					TokenDecimals:         18,
					TokenType:             string(model.TokenTypeNative),
				},
			},
		}
	}

	testCases := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "sol-overlap-addr",
			signatures: []event.SignatureInfo{
				{Hash: "sol-overlap-1", Sequence: 30},
				{Hash: " sol-overlap-1 ", Sequence: 30},
				{Hash: "sol-overlap-2", Sequence: 31},
				{Hash: "sol-overlap-2", Sequence: 31},
			},
			expectedTxs:    []string{"sol-overlap-1", "sol-overlap-2"},
			expectedCursor: "sol-overlap-2",
			responseA: []*sidecarv1.TransactionResult{
				buildTransfer("sol-overlap-1", 30, "sol-overlap-addr", "SOL"),
				buildTransfer(" sol-overlap-1 ", 30, "sol-overlap-addr", "SOL"),
				buildTransfer("sol-overlap-2", 31, "sol-overlap-addr", "SOL"),
			},
			responseB: []*sidecarv1.TransactionResult{
				buildTransfer("sol-overlap-2", 31, "sol-overlap-addr", "SOL"),
				buildTransfer("sol-overlap-1", 30, "sol-overlap-addr", "SOL"),
				buildTransfer(" sol-overlap-1 ", 30, "sol-overlap-addr", "SOL"),
			},
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			signatures: []event.SignatureInfo{
				{Hash: "ABCDEF", Sequence: 40},
				{Hash: "0xABCDEF", Sequence: 40},
				{Hash: "0x123", Sequence: 41},
				{Hash: "123", Sequence: 41},
			},
			expectedTxs:    []string{"0xabcdef", "0x123"},
			expectedCursor: "0x123",
			responseA: []*sidecarv1.TransactionResult{
				buildTransfer("0xabcdef", 40, "0x1111111111111111111111111111111111111111", "ETH"),
				buildTransfer("ABCDEF", 40, "0x1111111111111111111111111111111111111111", "ETH"),
				buildTransfer("0x123", 41, "0x1111111111111111111111111111111111111111", "ETH"),
			},
			responseB: []*sidecarv1.TransactionResult{
				buildTransfer("123", 41, "0x1111111111111111111111111111111111111111", "ETH"),
				buildTransfer("ABCDEF", 40, "0x1111111111111111111111111111111111111111", "ETH"),
				buildTransfer("0xabcdef", 40, "0x1111111111111111111111111111111111111111", "ETH"),
			},
		},
	}

	txHashes := func(batch event.NormalizedBatch) []string {
		out := make([]string, 0, len(batch.Transactions))
		for _, tx := range batch.Transactions {
			out = append(out, tx.TxHash)
		}
		return out
	}

	assertNoDuplicateCanonicalIDs := func(t *testing.T, batch event.NormalizedBatch) {
		t.Helper()
		seen := make(map[string]struct{})
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				require.NotEmpty(t, be.EventID)
				_, exists := seen[be.EventID]
				require.False(t, exists, "duplicate canonical event id found: %s", be.EventID)
				seen[be.EventID] = struct{}{}
			}
		}
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)
			normalizedCh := make(chan event.NormalizedBatch, 2)
			n := &Normalizer{
				sidecarTimeout: 30 * time.Second,
				normalizedCh:   normalizedCh,
				logger:         slog.Default(),
			}

			rawTxs := make([]json.RawMessage, len(tc.signatures))
			for i := range rawTxs {
				rawTxs[i] = json.RawMessage(`{"tx":"overlap"}`)
			}

			batch := event.RawBatch{
				Chain:           tc.chain,
				Network:         tc.network,
				Address:         tc.address,
				RawTransactions: rawTxs,
				Signatures:      tc.signatures,
			}

			call := 0
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(2).
				DoAndReturn(func(context.Context, *sidecarv1.DecodeSolanaTransactionBatchRequest, ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					call++
					if call == 1 {
						return &sidecarv1.DecodeSolanaTransactionBatchResponse{Results: tc.responseA}, nil
					}
					return &sidecarv1.DecodeSolanaTransactionBatchResponse{Results: tc.responseB}, nil
				})

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			first := <-normalizedCh
			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			second := <-normalizedCh

			assert.Equal(t, tc.expectedTxs, txHashes(first))
			assert.Equal(t, tc.expectedTxs, txHashes(second))
			assert.Equal(t, orderedCanonicalTuples(first), orderedCanonicalTuples(second))
			assertNoDuplicateCanonicalIDs(t, first)
			assertNoDuplicateCanonicalIDs(t, second)
			require.NotNil(t, first.NewCursorValue)
			require.NotNil(t, second.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *first.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *second.NewCursorValue)
		})
	}
}

func TestProcessBatch_MixedFinalityOverlapConvergesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name           string
		chain          model.Chain
		network        model.Network
		address        string
		signatures     []event.SignatureInfo
		expectedTxs    []string
		expectedCursor string
		responseA      []*sidecarv1.TransactionResult
		responseB      []*sidecarv1.TransactionResult
	}

	buildTransfer := func(txHash string, cursor int64, address string, contract string, metadata map[string]string) *sidecarv1.TransactionResult {
		return &sidecarv1.TransactionResult{
			TxHash:      txHash,
			BlockCursor: cursor,
			Status:      "SUCCESS",
			FeeAmount:   "1",
			FeePayer:    address,
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					OuterInstructionIndex: 0,
					InnerInstructionIndex: -1,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "transfer",
					ProgramId:             "program",
					Address:               address,
					ContractAddress:       contract,
					Delta:                 "-1",
					TokenSymbol:           "ETH",
					TokenName:             "Ether",
					TokenDecimals:         18,
					TokenType:             string(model.TokenTypeNative),
					Metadata:              metadata,
				},
			},
		}
	}

	testCases := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "sol-finality-addr",
			signatures: []event.SignatureInfo{
				{Hash: "sol-finality-1", Sequence: 50},
				{Hash: "sol-finality-1", Sequence: 50},
				{Hash: "sol-finality-2", Sequence: 51},
				{Hash: "sol-finality-2", Sequence: 51},
			},
			expectedTxs:    []string{"sol-finality-1", "sol-finality-2"},
			expectedCursor: "sol-finality-2",
			responseA: []*sidecarv1.TransactionResult{
				buildTransfer("sol-finality-1", 50, "sol-finality-addr", "SOL", map[string]string{"commitment": "confirmed"}),
				buildTransfer("sol-finality-1", 50, "sol-finality-addr", "SOL", map[string]string{"commitment": "finalized"}),
				buildTransfer("sol-finality-2", 51, "sol-finality-addr", "SOL", map[string]string{"commitment": "processed"}),
				buildTransfer("sol-finality-2", 51, "sol-finality-addr", "SOL", map[string]string{"commitment": "finalized"}),
			},
			responseB: []*sidecarv1.TransactionResult{
				buildTransfer("sol-finality-2", 51, "sol-finality-addr", "SOL", map[string]string{"commitment": "confirmed"}),
				buildTransfer("sol-finality-2", 51, "sol-finality-addr", "SOL", map[string]string{"commitment": "finalized"}),
				buildTransfer("sol-finality-1", 50, "sol-finality-addr", "SOL", map[string]string{"commitment": "processed"}),
				buildTransfer("sol-finality-1", 50, "sol-finality-addr", "SOL", map[string]string{"commitment": "finalized"}),
			},
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			signatures: []event.SignatureInfo{
				{Hash: "ABCDEF", Sequence: 60},
				{Hash: "0xABCDEF", Sequence: 60},
				{Hash: "0xBEEF", Sequence: 61},
				{Hash: "beef", Sequence: 61},
			},
			expectedTxs:    []string{"0xabcdef", "0xbeef"},
			expectedCursor: "0xbeef",
			responseA: []*sidecarv1.TransactionResult{
				buildTransfer("ABCDEF", 60, "0x1111111111111111111111111111111111111111", "ETH", map[string]string{"finality_state": "safe"}),
				buildTransfer("0xabcdef", 60, "0x1111111111111111111111111111111111111111", "ETH", map[string]string{"finality_state": "finalized"}),
				buildTransfer("0xBEEF", 61, "0x1111111111111111111111111111111111111111", "ETH", map[string]string{"finality": "latest"}),
				buildTransfer("beef", 61, "0x1111111111111111111111111111111111111111", "ETH", map[string]string{"finality": "finalized"}),
			},
			responseB: []*sidecarv1.TransactionResult{
				buildTransfer("0xabcdef", 60, "0x1111111111111111111111111111111111111111", "ETH", map[string]string{"finality_state": "finalized"}),
				buildTransfer("ABCDEF", 60, "0x1111111111111111111111111111111111111111", "ETH", map[string]string{"finality_state": "safe"}),
				buildTransfer("beef", 61, "0x1111111111111111111111111111111111111111", "ETH", map[string]string{"finality_state": "finalized"}),
				buildTransfer("0xBEEF", 61, "0x1111111111111111111111111111111111111111", "ETH", map[string]string{"finality_state": "safe"}),
			},
		},
	}

	txHashes := func(batch event.NormalizedBatch) []string {
		out := make([]string, 0, len(batch.Transactions))
		for _, tx := range batch.Transactions {
			out = append(out, tx.TxHash)
		}
		return out
	}

	assertNoDuplicateCanonicalIDs := func(t *testing.T, batch event.NormalizedBatch) {
		t.Helper()
		seen := make(map[string]struct{})
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				require.NotEmpty(t, be.EventID)
				_, exists := seen[be.EventID]
				require.False(t, exists, "duplicate canonical event id found: %s", be.EventID)
				seen[be.EventID] = struct{}{}
			}
		}
	}

	assertAllFinalized := func(t *testing.T, batch event.NormalizedBatch) {
		t.Helper()
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				assert.Equal(t, "finalized", be.FinalityState)
			}
		}
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)
			normalizedCh := make(chan event.NormalizedBatch, 2)
			n := &Normalizer{
				sidecarTimeout: 30 * time.Second,
				normalizedCh:   normalizedCh,
				logger:         slog.Default(),
			}

			rawTxs := make([]json.RawMessage, len(tc.signatures))
			for i := range rawTxs {
				rawTxs[i] = json.RawMessage(`{"tx":"mixed-finality-overlap"}`)
			}

			batch := event.RawBatch{
				Chain:           tc.chain,
				Network:         tc.network,
				Address:         tc.address,
				RawTransactions: rawTxs,
				Signatures:      tc.signatures,
			}

			call := 0
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(2).
				DoAndReturn(func(context.Context, *sidecarv1.DecodeSolanaTransactionBatchRequest, ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					call++
					if call == 1 {
						return &sidecarv1.DecodeSolanaTransactionBatchResponse{Results: tc.responseA}, nil
					}
					return &sidecarv1.DecodeSolanaTransactionBatchResponse{Results: tc.responseB}, nil
				})

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			first := <-normalizedCh
			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			second := <-normalizedCh

			assert.Equal(t, tc.expectedTxs, txHashes(first))
			assert.Equal(t, tc.expectedTxs, txHashes(second))
			assert.Equal(t, orderedCanonicalTuples(first), orderedCanonicalTuples(second))
			assertNoDuplicateCanonicalIDs(t, first)
			assertNoDuplicateCanonicalIDs(t, second)
			assertAllFinalized(t, first)
			assertAllFinalized(t, second)

			require.NotNil(t, first.NewCursorValue)
			require.NotNil(t, second.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *first.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *second.NewCursorValue)
		})
	}
}

func TestProcessBatch_PersistedArtifactsReplayHasZeroCanonicalTupleDiff(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)
	response := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			loadSolanaFixture(t, "solana_cpi_ownership_scoped.json"),
		},
	}
	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Times(2).
		Return(response, nil)

	runNormalizerOnce := func() event.NormalizedBatch {
		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30_000_000_000, // 30s
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		batch := event.RawBatch{
			Chain:   model.ChainSolana,
			Network: model.NetworkDevnet,
			Address: "addr_owner",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"sig_scope_1"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "sig_scope_1", Sequence: 100},
			},
		}

		err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
		require.NoError(t, err)

		return <-normalizedCh
	}

	persistedDir := t.TempDir()
	persistedPath := filepath.Join(persistedDir, "normalized-batch.json")

	first := runNormalizerOnce()
	persistedPayload, err := json.Marshal(first)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(persistedPath, persistedPayload, 0o600))

	second := runNormalizerOnce()

	var persisted event.NormalizedBatch
	persistedRaw, err := os.ReadFile(persistedPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(persistedRaw, &persisted))

	persistedTuples := orderedCanonicalTuples(persisted)
	replayTuples := orderedCanonicalTuples(second)
	assert.Equal(t, persistedTuples, replayTuples)
}

func TestProcessBatch_DualChainReplaySmoke_NoDuplicateCanonicalIDsAndCursorMonotonic(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 4)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	solResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			loadSolanaFixture(t, "solana_cpi_ownership_scoped.json"),
		},
	}
	baseResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			loadBaseFeeFixture(t, "base_fee_decomposition_complete.json"),
		},
	}

	const solSig = "sol-replay-1"
	const baseSig = "base-replay-1"
	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any()).
		Times(4).
		DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
			require.Len(t, req.GetTransactions(), 1)
			switch req.GetTransactions()[0].GetSignature() {
			case solSig:
				return solResp, nil
			case baseSig:
				return baseResp, nil
			default:
				t.Fatalf("unexpected signature %q", req.GetTransactions()[0].GetSignature())
				return nil, nil
			}
		})

	solPrev := "sol-prev"
	basePrev := "base-prev"
	solBatch := event.RawBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		Address:                "sol-addr-1",
		PreviousCursorValue:    &solPrev,
		PreviousCursorSequence: 10,
		NewCursorValue:         strPtr(solSig),
		NewCursorSequence:      11,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"sol-replay-1"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: solSig, Sequence: 11},
		},
	}
	baseBatch := event.RawBatch{
		Chain:                  model.ChainBase,
		Network:                model.NetworkSepolia,
		Address:                "0xbase-addr-1",
		PreviousCursorValue:    &basePrev,
		PreviousCursorSequence: 20,
		NewCursorValue:         strPtr(baseSig),
		NewCursorSequence:      21,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"base-replay-1"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: baseSig, Sequence: 21},
		},
	}

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, solBatch))
	firstSol := <-normalizedCh
	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, baseBatch))
	firstBase := <-normalizedCh

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, solBatch))
	secondSol := <-normalizedCh
	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, baseBatch))
	secondBase := <-normalizedCh

	assertRunNoDuplicateCanonicalIDs := func(run []event.NormalizedBatch) {
		seen := make(map[string]struct{})
		for _, batch := range run {
			for _, tx := range batch.Transactions {
				for _, be := range tx.BalanceEvents {
					require.NotEmpty(t, be.EventID)
					_, exists := seen[be.EventID]
					require.False(t, exists, "duplicate canonical event id in run: %s", be.EventID)
					seen[be.EventID] = struct{}{}
				}
			}
		}
	}

	firstRun := []event.NormalizedBatch{firstSol, firstBase}
	secondRun := []event.NormalizedBatch{secondSol, secondBase}
	assertRunNoDuplicateCanonicalIDs(firstRun)
	assertRunNoDuplicateCanonicalIDs(secondRun)
	assert.Equal(t, orderedCanonicalEventIDs(firstRun), orderedCanonicalEventIDs(secondRun), "canonical event ordering changed across identical replay inputs")

	firstRunIDs := make(map[string]struct{})
	secondRunIDs := make(map[string]struct{})
	for _, batch := range firstRun {
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				firstRunIDs[be.EventID] = struct{}{}
			}
		}
	}
	for _, batch := range secondRun {
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				secondRunIDs[be.EventID] = struct{}{}
			}
		}
	}
	assert.Equal(t, firstRunIDs, secondRunIDs, "canonical event id set changed across identical replay inputs")

	assert.GreaterOrEqual(t, firstSol.NewCursorSequence, firstSol.PreviousCursorSequence)
	assert.GreaterOrEqual(t, firstBase.NewCursorSequence, firstBase.PreviousCursorSequence)
	assert.GreaterOrEqual(t, secondSol.NewCursorSequence, secondSol.PreviousCursorSequence)
	assert.GreaterOrEqual(t, secondBase.NewCursorSequence, secondBase.PreviousCursorSequence)
	assert.GreaterOrEqual(t, secondSol.NewCursorSequence, firstSol.NewCursorSequence)
	assert.GreaterOrEqual(t, secondBase.NewCursorSequence, firstBase.NewCursorSequence)
}

func strPtr(v string) *string {
	return &v
}

func TestProcessBatch_BaseSepoliaFeeDecomposition_DeterministicComponents(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	batch := event.RawBatch{
		Chain:   model.ChainBase,
		Network: model.NetworkSepolia,
		Address: "base_addr_1",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"base-1"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "baseSig1", Sequence: 1},
		},
	}

	txResult := loadBaseFeeFixture(t, "base_fee_decomposition_complete.json")
	response := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{txResult},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(response, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)

	result := <-normalizedCh
	require.Len(t, result.Transactions, 1)

	tx := result.Transactions[0]
	execEvents := fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeExecutionL2)
	dataEvents := fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeDataL1)

	require.Len(t, execEvents, 1)
	require.Len(t, dataEvents, 1)

	execEvent := execEvents[0]
	dataEvent := dataEvents[0]

	assert.Equal(t, model.EventCategoryFeeExecutionL2, execEvent.EventCategory)
	assert.Equal(t, "fee_execution_l2", execEvent.EventAction)
	assert.Equal(t, "-600", execEvent.Delta)
	assert.Equal(t, "log:42", execEvent.EventPath)
	assert.Equal(t, "base_log", execEvent.EventPathType)

	assert.Equal(t, model.EventCategoryFeeDataL1, dataEvent.EventCategory)
	assert.Equal(t, "fee_data_l1", dataEvent.EventAction)
	assert.Equal(t, "-400", dataEvent.Delta)

	dataChainData := map[string]string{}
	require.NoError(t, json.Unmarshal(dataEvent.ChainData, &dataChainData))
	assert.Empty(t, dataChainData)

	execChainData := map[string]string{}
	require.NoError(t, json.Unmarshal(execEvent.ChainData, &execChainData))
	assert.Empty(t, execChainData)
}

func TestProcessBatch_BaseSepoliaFeeDecomposition_MissingL1DataFee(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	batch := event.RawBatch{
		Chain:   model.ChainBase,
		Network: model.NetworkSepolia,
		Address: "base_addr_1",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"base-2"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "baseSig2", Sequence: 2},
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
			Results: []*sidecarv1.TransactionResult{
				loadBaseFeeFixture(t, "base_fee_decomposition_missing_l1.json"),
			},
		}, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)

	result := <-normalizedCh
	require.Len(t, result.Transactions, 1)
	tx := result.Transactions[0]

	execEvents := fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeExecutionL2)
	dataEvents := fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeDataL1)
	require.Len(t, execEvents, 1)
	require.Len(t, dataEvents, 0)

	execEvent := execEvents[0]
	chainData := map[string]string{}
	require.NoError(t, json.Unmarshal(execEvent.ChainData, &chainData))
	assert.Equal(t, "true", chainData["data_fee_l1_unavailable"])
	assert.Equal(t, "fee_execution_l2", execEvent.EventAction)
	assert.Equal(t, "-1000", execEvent.Delta)
}

func TestProcessBatch_BaseSepoliaFeeDecomposition_TotalMismatchMarker(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	batch := event.RawBatch{
		Chain:   model.ChainBase,
		Network: model.NetworkSepolia,
		Address: "base_addr_1",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"base-4"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "baseSig4", Sequence: 4},
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
			Results: []*sidecarv1.TransactionResult{
				loadBaseFeeFixture(t, "base_fee_decomposition_mismatch.json"),
			},
		}, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)

	result := <-normalizedCh
	require.Len(t, result.Transactions, 1)

	execEvents := fixtureBaseFeeEventsByCategory(result.Transactions[0].BalanceEvents, model.EventCategoryFeeExecutionL2)
	require.Len(t, execEvents, 1)
	execEvent := execEvents[0]

	chainData := map[string]string{}
	require.NoError(t, json.Unmarshal(execEvent.ChainData, &chainData))
	assert.Equal(t, "true", chainData["fee_total_mismatch"])
	assert.Equal(t, "600", chainData["fee_execution_total_from_components"])
	assert.Equal(t, "500", chainData["fee_data_total_from_components"])
	assert.Equal(t, "1000", chainData["fee_amount_total"])
}

func TestProcessBatch_BaseSepoliaFeeDecomposition_ReplayProducesNoDuplicateFeeEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 2)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	batch := event.RawBatch{
		Chain:   model.ChainBase,
		Network: model.NetworkSepolia,
		Address: "base_addr_1",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"base-3"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "baseSig3", Sequence: 3},
		},
	}

	response := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			loadBaseFeeFixture(t, "base_fee_decomposition_complete.json"),
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Times(2).
		Return(response, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)
	first := <-normalizedCh

	err = n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)
	second := <-normalizedCh

	require.Len(t, first.Transactions, 1)
	require.Len(t, second.Transactions, 1)

	firstExec := fixtureBaseFeeEventsByCategory(first.Transactions[0].BalanceEvents, model.EventCategoryFeeExecutionL2)
	firstData := fixtureBaseFeeEventsByCategory(first.Transactions[0].BalanceEvents, model.EventCategoryFeeDataL1)
	secondExec := fixtureBaseFeeEventsByCategory(second.Transactions[0].BalanceEvents, model.EventCategoryFeeExecutionL2)
	secondData := fixtureBaseFeeEventsByCategory(second.Transactions[0].BalanceEvents, model.EventCategoryFeeDataL1)

	assert.Len(t, firstExec, 1)
	assert.Len(t, firstData, 1)
	assert.Len(t, secondExec, 1)
	assert.Len(t, secondData, 1)

	firstIDs := map[string]struct{}{}
	secondIDs := map[string]struct{}{}
	for _, be := range append(firstExec, firstData...) {
		firstIDs[be.EventID] = struct{}{}
	}
	for _, be := range append(secondExec, secondData...) {
		secondIDs[be.EventID] = struct{}{}
	}
	assert.Len(t, firstIDs, 2)
	assert.Len(t, secondIDs, 2)

	for k := range firstIDs {
		_, ok := secondIDs[k]
		assert.True(t, ok)
	}
}

func TestProcessBatch_SolanaFailedTransaction_EmitsFeeWithCompleteMetadata(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	const failedSig = "sol-failed-fee-1"
	errMsg := "instruction error"
	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
			Results: []*sidecarv1.TransactionResult{
				{
					TxHash:      failedSig,
					BlockCursor: 321,
					BlockTime:   1700000300,
					FeeAmount:   "7000",
					FeePayer:    "sol-fee-payer-1",
					Status:      "FAILED",
					Error:       &errMsg,
				},
			},
		}, nil)

	batch := event.RawBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "sol-fee-payer-1",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"sol-failed-fee-1"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: failedSig, Sequence: 321},
		},
	}

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
	result := <-normalizedCh

	require.Len(t, result.Transactions, 1)
	tx := result.Transactions[0]
	assert.Equal(t, model.TxStatusFailed, tx.Status)
	require.NotNil(t, tx.Err)
	assert.Equal(t, errMsg, *tx.Err)

	feeEvents := fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFee)
	require.Len(t, feeEvents, 1)
	assert.Equal(t, "transaction_fee", feeEvents[0].EventAction)
	assert.Equal(t, "sol-fee-payer-1", feeEvents[0].Address)
	assert.Equal(t, "-7000", feeEvents[0].Delta)
	assert.Equal(t, "outer:-1|inner:-1", feeEvents[0].EventPath)
	assert.NotEmpty(t, feeEvents[0].EventID)
}

func TestProcessBatch_SolanaFailedTransaction_IncompleteFeeMetadata_NoSyntheticFeeEvent(t *testing.T) {
	cases := []struct {
		name      string
		feeAmount string
		feePayer  string
	}{
		{
			name:      "missing_fee_payer",
			feeAmount: "7000",
			feePayer:  "",
		},
		{
			name:      "zero_fee_amount",
			feeAmount: "0",
			feePayer:  "sol-fee-payer-1",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)

			normalizedCh := make(chan event.NormalizedBatch, 1)
			n := &Normalizer{
				sidecarTimeout: 30_000_000_000, // 30s
				normalizedCh:   normalizedCh,
				logger:         slog.Default(),
			}

			const failedSig = "sol-failed-fee-incomplete"
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
					Results: []*sidecarv1.TransactionResult{
						{
							TxHash:      failedSig,
							BlockCursor: 322,
							FeeAmount:   tc.feeAmount,
							FeePayer:    tc.feePayer,
							Status:      "FAILED",
						},
					},
				}, nil)

			batch := event.RawBatch{
				Chain:   model.ChainSolana,
				Network: model.NetworkDevnet,
				Address: "sol-fee-payer-1",
				RawTransactions: []json.RawMessage{
					json.RawMessage(`{"tx":"sol-failed-fee-incomplete"}`),
				},
				Signatures: []event.SignatureInfo{
					{Hash: failedSig, Sequence: 322},
				},
			}

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			result := <-normalizedCh
			require.Len(t, result.Transactions, 1)
			assert.Equal(t, model.TxStatusFailed, result.Transactions[0].Status)
			assert.Len(t, fixtureBaseFeeEventsByCategory(result.Transactions[0].BalanceEvents, model.EventCategoryFee), 0)
			assert.Len(t, result.Transactions[0].BalanceEvents, 0)
		})
	}
}

func TestProcessBatch_BaseFailedTransaction_EmitsDeterministicFeeEventsWithCompleteMetadata(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	const failedSig = "base-failed-fee-1"
	txResult := loadBaseFeeFixture(t, "base_fee_decomposition_complete.json")
	txResult.TxHash = failedSig
	txResult.Status = "FAILED"

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
			Results: []*sidecarv1.TransactionResult{txResult},
		}, nil)

	batch := event.RawBatch{
		Chain:   model.ChainBase,
		Network: model.NetworkSepolia,
		Address: "0x1111111111111111111111111111111111111111",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"base-failed-fee-1"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: failedSig, Sequence: 401},
		},
	}

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
	result := <-normalizedCh

	require.Len(t, result.Transactions, 1)
	tx := result.Transactions[0]
	assert.Equal(t, model.TxStatusFailed, tx.Status)

	execEvents := fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeExecutionL2)
	dataEvents := fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeDataL1)
	require.Len(t, execEvents, 1)
	require.Len(t, dataEvents, 1)

	assert.Equal(t, "fee_execution_l2", execEvents[0].EventAction)
	assert.Equal(t, "-600", execEvents[0].Delta)
	assert.Equal(t, "log:42", execEvents[0].EventPath)
	assert.NotEmpty(t, execEvents[0].EventID)

	assert.Equal(t, "fee_data_l1", dataEvents[0].EventAction)
	assert.Equal(t, "-400", dataEvents[0].Delta)
	assert.Equal(t, "log:42", dataEvents[0].EventPath)
	assert.NotEmpty(t, dataEvents[0].EventID)
}

func TestProcessBatch_BaseFailedTransaction_IncompleteFeeMetadata_NoSyntheticFeeEvents(t *testing.T) {
	cases := []struct {
		name      string
		feeAmount string
		feePayer  string
	}{
		{
			name:      "missing_fee_payer",
			feeAmount: "1000",
			feePayer:  "",
		},
		{
			name:      "zero_fee_amount",
			feeAmount: "0",
			feePayer:  "0x1111111111111111111111111111111111111111",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)

			normalizedCh := make(chan event.NormalizedBatch, 1)
			n := &Normalizer{
				sidecarTimeout: 30_000_000_000, // 30s
				normalizedCh:   normalizedCh,
				logger:         slog.Default(),
			}

			txResult := loadBaseFeeFixture(t, "base_fee_decomposition_complete.json")
			txResult.TxHash = "base-failed-fee-incomplete"
			txResult.Status = "FAILED"
			txResult.FeeAmount = tc.feeAmount
			txResult.FeePayer = tc.feePayer

			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
					Results: []*sidecarv1.TransactionResult{txResult},
				}, nil)

			batch := event.RawBatch{
				Chain:   model.ChainBase,
				Network: model.NetworkSepolia,
				Address: "0x1111111111111111111111111111111111111111",
				RawTransactions: []json.RawMessage{
					json.RawMessage(`{"tx":"base-failed-fee-incomplete"}`),
				},
				Signatures: []event.SignatureInfo{
					{Hash: "base-failed-fee-incomplete", Sequence: 402},
				},
			}

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			result := <-normalizedCh
			require.Len(t, result.Transactions, 1)

			tx := result.Transactions[0]
			assert.Equal(t, model.TxStatusFailed, tx.Status)
			assert.Len(t, fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeExecutionL2), 0)
			assert.Len(t, fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeDataL1), 0)
		})
	}
}

func TestProcessBatch_DualChainReplaySmoke_MixedSuccessFailed_NoDuplicateCanonicalIDsAndCursorMonotonic(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 4)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	const (
		solSuccessSig  = "sol-mix-success-1"
		solFailedSig   = "sol-mix-failed-1"
		baseSuccessSig = "base-mix-success-1"
		baseFailedSig  = "base-mix-failed-1"
	)

	solSuccess := loadSolanaFixture(t, "solana_cpi_ownership_scoped.json")
	solSuccess.TxHash = solSuccessSig
	solFailed := &sidecarv1.TransactionResult{
		TxHash:      solFailedSig,
		BlockCursor: 121,
		BlockTime:   1700000051,
		FeeAmount:   "750",
		FeePayer:    "owner_addr",
		Status:      "FAILED",
	}
	baseSuccess := loadBaseFeeFixture(t, "base_fee_decomposition_complete.json")
	baseSuccess.TxHash = baseSuccessSig
	baseFailed := loadBaseFeeFixture(t, "base_fee_decomposition_complete.json")
	baseFailed.TxHash = baseFailedSig
	baseFailed.Status = "FAILED"

	solResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{solSuccess, solFailed},
	}
	baseResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{baseSuccess, baseFailed},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any()).
		Times(4).
		DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
			require.Len(t, req.GetTransactions(), 2)
			switch req.GetTransactions()[0].GetSignature() {
			case solSuccessSig:
				return solResp, nil
			case baseSuccessSig:
				return baseResp, nil
			default:
				t.Fatalf("unexpected signature %q", req.GetTransactions()[0].GetSignature())
				return nil, nil
			}
		})

	solPrev := "sol-prev-mixed"
	basePrev := "base-prev-mixed"
	solBatch := event.RawBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		Address:                "owner_addr",
		PreviousCursorValue:    &solPrev,
		PreviousCursorSequence: 300,
		NewCursorValue:         strPtr(solFailedSig),
		NewCursorSequence:      302,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"sol-mix-success-1"}`),
			json.RawMessage(`{"tx":"sol-mix-failed-1"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: solSuccessSig, Sequence: 301},
			{Hash: solFailedSig, Sequence: 302},
		},
	}
	baseBatch := event.RawBatch{
		Chain:                  model.ChainBase,
		Network:                model.NetworkSepolia,
		Address:                "0x1111111111111111111111111111111111111111",
		PreviousCursorValue:    &basePrev,
		PreviousCursorSequence: 500,
		NewCursorValue:         strPtr(baseFailedSig),
		NewCursorSequence:      502,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"base-mix-success-1"}`),
			json.RawMessage(`{"tx":"base-mix-failed-1"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: baseSuccessSig, Sequence: 501},
			{Hash: baseFailedSig, Sequence: 502},
		},
	}

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, solBatch))
	firstSol := <-normalizedCh
	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, baseBatch))
	firstBase := <-normalizedCh

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, solBatch))
	secondSol := <-normalizedCh
	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, baseBatch))
	secondBase := <-normalizedCh

	assertRunNoDuplicateCanonicalIDs := func(run []event.NormalizedBatch) {
		seen := make(map[string]struct{})
		for _, batch := range run {
			for _, tx := range batch.Transactions {
				for _, be := range tx.BalanceEvents {
					require.NotEmpty(t, be.EventID)
					_, exists := seen[be.EventID]
					require.False(t, exists, "duplicate canonical event id in run: %s", be.EventID)
					seen[be.EventID] = struct{}{}
				}
			}
		}
	}

	firstRun := []event.NormalizedBatch{firstSol, firstBase}
	secondRun := []event.NormalizedBatch{secondSol, secondBase}
	assertRunNoDuplicateCanonicalIDs(firstRun)
	assertRunNoDuplicateCanonicalIDs(secondRun)
	assert.Equal(t, orderedCanonicalEventIDs(firstRun), orderedCanonicalEventIDs(secondRun), "canonical event ordering changed across mixed status replay inputs")

	require.NotNil(t, firstSol.NewCursorValue)
	require.NotNil(t, firstBase.NewCursorValue)
	assert.Equal(t, solFailedSig, *firstSol.NewCursorValue)
	assert.Equal(t, baseFailedSig, *firstBase.NewCursorValue)

	assert.GreaterOrEqual(t, firstSol.NewCursorSequence, firstSol.PreviousCursorSequence)
	assert.GreaterOrEqual(t, firstBase.NewCursorSequence, firstBase.PreviousCursorSequence)
	assert.GreaterOrEqual(t, secondSol.NewCursorSequence, secondSol.PreviousCursorSequence)
	assert.GreaterOrEqual(t, secondBase.NewCursorSequence, secondBase.PreviousCursorSequence)
	assert.GreaterOrEqual(t, secondSol.NewCursorSequence, firstSol.NewCursorSequence)
	assert.GreaterOrEqual(t, secondBase.NewCursorSequence, firstBase.NewCursorSequence)
}

func TestBuildCanonicalEventID_Stable(t *testing.T) {
	id1 := buildCanonicalEventID(
		model.ChainSolana, model.NetworkDevnet,
		"sig1", "outer:0|inner:-1", "addr1", "So11111111111111111111111111111111111111112", model.EventCategoryTransfer,
	)
	id2 := buildCanonicalEventID(
		model.ChainSolana, model.NetworkDevnet,
		"sig1", "outer:0|inner:-1", "addr1", "So11111111111111111111111111111111111111112", model.EventCategoryTransfer,
	)

	assert.Equal(t, id1, id2)
	assert.Len(t, id1, 64)
}

func TestProcessBatch_SolInstructionOwnershipDedup(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
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
				{
					TxHash:      "sig1",
					BlockCursor: 100,
					FeeAmount:   "5000",
					FeePayer:    "addr1",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: 1,
							EventCategory:         "TRANSFER",
							EventAction:           "inner_cpi_transfer",
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
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         "TRANSFER",
							EventAction:           "outer_owner_transfer",
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
			},
		}, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)

	result := <-normalizedCh
	require.Len(t, result.Transactions, 1)
	assert.Equal(t, model.TxStatusSuccess, result.Transactions[0].Status)
	require.Len(t, result.Transactions[0].BalanceEvents, 2)

	transferEvent := result.Transactions[0].BalanceEvents[0]
	if transferEvent.EventCategory == model.EventCategoryFee {
		transferEvent = result.Transactions[0].BalanceEvents[1]
	}
	assert.Equal(t, model.EventCategoryTransfer, transferEvent.EventCategory)
	assert.Equal(t, "outer_owner_transfer", transferEvent.EventAction)
	assert.Equal(t, "outer:0|inner:-1", transferEvent.EventPath)
}

func TestProcessBatch_SolInstructionOwnershipDedupByAddressAsset(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	batch := event.RawBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr_owner",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":1}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "sig_scope_1", Sequence: 100},
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
			Results: []*sidecarv1.TransactionResult{
				loadSolanaFixture(t, "solana_cpi_ownership_scoped.json"),
			},
		}, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)

	result := <-normalizedCh
	require.Len(t, result.Transactions, 1)
	tx := result.Transactions[0]
	assert.Equal(t, model.TxStatusSuccess, tx.Status)

	transferByAction := map[string]event.NormalizedBalanceEvent{}
	eventPaths := map[string]struct{}{}
	for _, be := range tx.BalanceEvents {
		if be.EventCategory == model.EventCategoryTransfer {
			transferByAction[be.EventAction] = be
			_, dup := eventPaths[be.EventPath]
			require.False(t, dup, "duplicate event path %s", be.EventPath)
			eventPaths[be.EventPath] = struct{}{}
		}
	}
	assert.Equal(t, "outer:0|inner:-1", transferByAction["outer_owner_transfer"].EventPath)
	assert.Equal(t, "outer:0|inner:1", transferByAction["inner_non_owner_transfer"].EventPath)
	assert.Equal(t, model.EventCategoryTransfer, transferByAction["inner_non_owner_transfer"].EventCategory)
	assert.Equal(t, "-500", transferByAction["outer_owner_transfer"].Delta)
	assert.Equal(t, "-250", transferByAction["inner_non_owner_transfer"].Delta)
	assert.Equal(t, 3, len(tx.BalanceEvents))

	var feeEvent event.NormalizedBalanceEvent
	for _, be := range tx.BalanceEvents {
		if be.EventCategory == model.EventCategoryFee {
			feeEvent = be
			break
		}
	}
	assert.Equal(t, model.EventCategoryFee, feeEvent.EventCategory)
	assert.Equal(t, "outer:-1|inner:-1", feeEvent.EventPath)
	assert.NotEmpty(t, feeEvent.EventID)
}

func TestProcessBatch_SolanaReplayTuplesStableForCPIDedupFixture(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 2)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	batch := event.RawBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr_owner",
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":1}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "sig_scope_1", Sequence: 100},
		},
	}

	response := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			loadSolanaFixture(t, "solana_cpi_ownership_scoped.json"),
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Times(2).
		Return(response, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)
	first := <-normalizedCh

	err = n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)
	second := <-normalizedCh

	require.Len(t, first.Transactions, 1)
	require.Len(t, second.Transactions, 1)

	eventTuple := func(tx event.NormalizedTransaction) [][3]string {
		out := make([][3]string, len(tx.BalanceEvents))
		for i, be := range tx.BalanceEvents {
			out[i] = [3]string{be.EventID, be.Delta, string(be.EventCategory)}
		}
		return out
	}

	assert.Equal(t, eventTuple(first.Transactions[0]), eventTuple(second.Transactions[0]))
}

func TestProcessBatch_SolanaFeeEventIsSignedDebit(t *testing.T) {
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
			Results: []*sidecarv1.TransactionResult{
				{
					TxHash:      "sig1",
					BlockCursor: 100,
					FeeAmount:   "5000",
					FeePayer:    "addr1",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 2,
							InnerInstructionIndex: 0,
							EventCategory:         "FEE",
							EventAction:           "transaction_fee",
							ProgramId:             "11111111111111111111111111111111",
							Address:               "addr1",
							ContractAddress:       "11111111111111111111111111111111",
							Delta:                 "+5000",
							CounterpartyAddress:   "",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             "NATIVE",
							Metadata:              map[string]string{},
						},
					},
				},
			},
		}, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)
	result := <-normalizedCh

	require.Len(t, result.Transactions, 1)
	assert.Equal(t, model.TxStatusSuccess, result.Transactions[0].Status)
	require.Len(t, result.Transactions[0].BalanceEvents, 1)

	feeEvent := result.Transactions[0].BalanceEvents[0]
	assert.Equal(t, model.EventCategoryFee, feeEvent.EventCategory)
	assert.Equal(t, "addr1", feeEvent.Address)
	assert.Equal(t, "-5000", feeEvent.Delta)
	assert.Equal(t, "outer:-1|inner:-1", feeEvent.EventPath)
	assert.Equal(t, "transaction_fee", feeEvent.EventAction)
	assert.NotEmpty(t, feeEvent.EventID)
}

func TestProcessBatch_ReplayTuplesStableForSCPersistentOrdering(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 2)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000, // 30s
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

	response := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			{
				TxHash:      "sig1",
				BlockCursor: 100,
				FeeAmount:   "5000",
				FeePayer:    "addr1",
				Status:      "SUCCESS",
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: 2,
						EventCategory:         "TRANSFER",
						EventAction:           "inner_repeat",
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
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						EventCategory:         "TRANSFER",
						EventAction:           "outer_repeat",
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
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Times(2).
		Return(response, nil)

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)
	first := <-normalizedCh

	err = n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.NoError(t, err)
	second := <-normalizedCh

	require.Len(t, first.Transactions, 1)
	require.Len(t, second.Transactions, 1)
	require.Len(t, first.Transactions[0].BalanceEvents, 2)
	require.Len(t, second.Transactions[0].BalanceEvents, 2)

	tuples := func(tx event.NormalizedTransaction) [][3]string {
		out := make([][3]string, len(tx.BalanceEvents))
		for i, be := range tx.BalanceEvents {
			out[i] = [3]string{
				be.EventID,
				be.Delta,
				string(be.EventCategory),
			}
		}
		return out
	}

	assert.Equal(t, tuples(first.Transactions[0]), tuples(second.Transactions[0]))
}

func TestProcessBatch_DecodeCollapseFailFastDeterministicAcrossMandatoryChains(t *testing.T) {
	testCases := []struct {
		name      string
		chain     model.Chain
		network   model.Network
		address   string
		firstSig  string
		secondSig string
	}{
		{
			name:      "solana-devnet",
			chain:     model.ChainSolana,
			network:   model.NetworkDevnet,
			address:   "addr_owner",
			firstSig:  "sol-collapse-1",
			secondSig: "sol-collapse-2",
		},
		{
			name:      "base-sepolia",
			chain:     model.ChainBase,
			network:   model.NetworkSepolia,
			address:   "0x1111111111111111111111111111111111111111",
			firstSig:  "base-collapse-1",
			secondSig: "base-collapse-2",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)

			normalizedCh := make(chan event.NormalizedBatch, 1)
			n := &Normalizer{
				sidecarTimeout: 30_000_000_000,
				normalizedCh:   normalizedCh,
				logger:         slog.Default(),
			}

			batch := event.RawBatch{
				Chain:   tc.chain,
				Network: tc.network,
				Address: tc.address,
				RawTransactions: []json.RawMessage{
					json.RawMessage(`{"tx":1}`),
					json.RawMessage(`{"tx":2}`),
				},
				Signatures: []event.SignatureInfo{
					{Hash: tc.firstSig, Sequence: 100},
					{Hash: tc.secondSig, Sequence: 101},
				},
			}

			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(2).
				Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
					Errors: []*sidecarv1.DecodeError{
						{Signature: tc.firstSig, Error: "parse error"},
						{Signature: tc.secondSig, Error: "schema mismatch"},
					},
				}, nil)

			errFirst := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
			errSecond := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
			require.Error(t, errFirst)
			require.Error(t, errSecond)
			assert.Equal(t, errFirst.Error(), errSecond.Error(), "decode collapse diagnostics should be deterministic")
			assert.Contains(t, errFirst.Error(), "decode collapse stage=normalizer.decode_batch")
			assert.Contains(t, errFirst.Error(), tc.firstSig+"=parse error")
			assert.Contains(t, errFirst.Error(), tc.secondSig+"=schema mismatch")
			assert.Equal(t, 0, len(normalizedCh), "full-batch decode collapse must not emit normalized output")
		})
	}
}

func TestProcessBatch_DualChainReplaySmoke_MixedSuccessDecodeFailure_NoDuplicateCanonicalIDsAndCursorMonotonic(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 4)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000,
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	const (
		solPrefixSig = "sol-decode-ok-1"
		solFailedSig = "sol-decode-failed-1"
		solSuffixSig = "sol-decode-ok-2"

		basePrefixSig = "base-decode-ok-1"
		baseFailedSig = "base-decode-failed-1"
		baseSuffixSig = "base-decode-ok-2"
	)

	solPrefix := loadSolanaFixture(t, "solana_cpi_ownership_scoped.json")
	solPrefix.TxHash = solPrefixSig
	solSuffix := loadSolanaFixture(t, "solana_cpi_ownership_scoped.json")
	solSuffix.TxHash = solSuffixSig
	solSuffix.BlockCursor = 403

	basePrefix := loadBaseFeeFixture(t, "base_fee_decomposition_complete.json")
	basePrefix.TxHash = basePrefixSig
	baseSuffix := loadBaseFeeFixture(t, "base_fee_decomposition_complete.json")
	baseSuffix.TxHash = baseSuffixSig
	baseSuffix.BlockCursor = 703

	solResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{solPrefix, solSuffix},
		Errors: []*sidecarv1.DecodeError{
			{Signature: solFailedSig, Error: "parse error"},
		},
	}
	baseResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{basePrefix, baseSuffix},
		Errors: []*sidecarv1.DecodeError{
			{Signature: baseFailedSig, Error: "parse error"},
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any()).
		Times(4).
		DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
			require.Len(t, req.GetTransactions(), 3)
			switch req.GetTransactions()[0].GetSignature() {
			case solPrefixSig:
				return solResp, nil
			case basePrefixSig:
				return baseResp, nil
			default:
				t.Fatalf("unexpected signature %q", req.GetTransactions()[0].GetSignature())
				return nil, nil
			}
		})

	solPrev := "sol-prev-decode-mixed"
	basePrev := "base-prev-decode-mixed"
	solBatch := event.RawBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		Address:                "addr_owner",
		PreviousCursorValue:    &solPrev,
		PreviousCursorSequence: 400,
		NewCursorValue:         strPtr(solSuffixSig),
		NewCursorSequence:      403,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"sol-decode-ok-1"}`),
			json.RawMessage(`{"tx":"sol-decode-failed-1"}`),
			json.RawMessage(`{"tx":"sol-decode-ok-2"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: solPrefixSig, Sequence: 401},
			{Hash: solFailedSig, Sequence: 402},
			{Hash: solSuffixSig, Sequence: 403},
		},
	}
	baseBatch := event.RawBatch{
		Chain:                  model.ChainBase,
		Network:                model.NetworkSepolia,
		Address:                "0x1111111111111111111111111111111111111111",
		PreviousCursorValue:    &basePrev,
		PreviousCursorSequence: 700,
		NewCursorValue:         strPtr(baseSuffixSig),
		NewCursorSequence:      703,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"base-decode-ok-1"}`),
			json.RawMessage(`{"tx":"base-decode-failed-1"}`),
			json.RawMessage(`{"tx":"base-decode-ok-2"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: basePrefixSig, Sequence: 701},
			{Hash: baseFailedSig, Sequence: 702},
			{Hash: baseSuffixSig, Sequence: 703},
		},
	}

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, solBatch))
	firstSol := <-normalizedCh
	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, baseBatch))
	firstBase := <-normalizedCh

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, solBatch))
	secondSol := <-normalizedCh
	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, baseBatch))
	secondBase := <-normalizedCh

	txHashes := func(batch event.NormalizedBatch) []string {
		out := make([]string, 0, len(batch.Transactions))
		for _, tx := range batch.Transactions {
			out = append(out, tx.TxHash)
		}
		return out
	}
	assertRunNoDuplicateCanonicalIDs := func(run []event.NormalizedBatch) {
		seen := make(map[string]struct{})
		for _, batch := range run {
			for _, tx := range batch.Transactions {
				for _, be := range tx.BalanceEvents {
					require.NotEmpty(t, be.EventID)
					_, exists := seen[be.EventID]
					require.False(t, exists, "duplicate canonical event id in run: %s", be.EventID)
					seen[be.EventID] = struct{}{}
				}
			}
		}
	}

	require.Len(t, firstSol.Transactions, 2)
	require.Len(t, firstBase.Transactions, 2)
	assert.Equal(t, []string{solPrefixSig, solSuffixSig}, txHashes(firstSol))
	assert.Equal(t, []string{basePrefixSig, baseSuffixSig}, txHashes(firstBase))
	assert.NotContains(t, txHashes(firstSol), solFailedSig)
	assert.NotContains(t, txHashes(firstBase), baseFailedSig)

	firstRun := []event.NormalizedBatch{firstSol, firstBase}
	secondRun := []event.NormalizedBatch{secondSol, secondBase}
	assertRunNoDuplicateCanonicalIDs(firstRun)
	assertRunNoDuplicateCanonicalIDs(secondRun)
	assert.Equal(t, orderedCanonicalEventIDs(firstRun), orderedCanonicalEventIDs(secondRun), "canonical event ordering changed across mixed success+decode-failure replay inputs")

	require.NotNil(t, firstSol.NewCursorValue)
	require.NotNil(t, firstBase.NewCursorValue)
	assert.Equal(t, solSuffixSig, *firstSol.NewCursorValue)
	assert.Equal(t, baseSuffixSig, *firstBase.NewCursorValue)

	assert.GreaterOrEqual(t, firstSol.NewCursorSequence, firstSol.PreviousCursorSequence)
	assert.GreaterOrEqual(t, firstBase.NewCursorSequence, firstBase.PreviousCursorSequence)
	assert.GreaterOrEqual(t, secondSol.NewCursorSequence, secondSol.PreviousCursorSequence)
	assert.GreaterOrEqual(t, secondBase.NewCursorSequence, secondBase.PreviousCursorSequence)
	assert.GreaterOrEqual(t, secondSol.NewCursorSequence, firstSol.NewCursorSequence)
	assert.GreaterOrEqual(t, secondBase.NewCursorSequence, firstBase.NewCursorSequence)
}

func TestProcessBatchWithRetry_TransientDecodeFailureDeterministicAcrossChains(t *testing.T) {
	testCases := []struct {
		name        string
		chain       model.Chain
		network     model.Network
		address     string
		signature   string
		sequence    int64
		buildResult func(*testing.T) *sidecarv1.TransactionResult
	}{
		{
			name:      "solana-devnet",
			chain:     model.ChainSolana,
			network:   model.NetworkDevnet,
			address:   "addr_owner",
			signature: "sig_scope_1",
			sequence:  100,
			buildResult: func(t *testing.T) *sidecarv1.TransactionResult {
				return loadSolanaFixture(t, "solana_cpi_ownership_scoped.json")
			},
		},
		{
			name:      "base-sepolia",
			chain:     model.ChainBase,
			network:   model.NetworkSepolia,
			address:   "0x1111111111111111111111111111111111111111",
			signature: "baseSig1",
			sequence:  200,
			buildResult: func(t *testing.T) *sidecarv1.TransactionResult {
				return loadBaseFeeFixture(t, "base_fee_decomposition_complete.json")
			},
		},
	}

	assertNoDuplicateCanonicalIDs := func(t *testing.T, batch event.NormalizedBatch) {
		t.Helper()
		seen := make(map[string]struct{})
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				require.NotEmpty(t, be.EventID)
				_, exists := seen[be.EventID]
				require.False(t, exists, "duplicate canonical event id in retry output: %s", be.EventID)
				seen[be.EventID] = struct{}{}
			}
		}
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)

			normalizedCh := make(chan event.NormalizedBatch, 2)
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
				Chain:             tc.chain,
				Network:           tc.network,
				Address:           tc.address,
				RawTransactions:   []json.RawMessage{json.RawMessage(`{"tx":"retry"}`)},
				Signatures:        []event.SignatureInfo{{Hash: tc.signature, Sequence: tc.sequence}},
				NewCursorValue:    strPtr(tc.signature),
				NewCursorSequence: tc.sequence,
			}

			decodeAttempts := 0
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(4).
				DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					require.Len(t, req.Transactions, 1)
					assert.Equal(t, tc.signature, req.Transactions[0].Signature)
					decodeAttempts++
					if decodeAttempts%2 == 1 {
						return nil, errors.New("sidecar unavailable")
					}
					return &sidecarv1.DecodeSolanaTransactionBatchResponse{
						Results: []*sidecarv1.TransactionResult{
							tc.buildResult(t),
						},
					}, nil
				})

			require.NoError(t, n.processBatchWithRetry(context.Background(), slog.Default(), mockClient, batch))
			first := <-normalizedCh
			require.NoError(t, n.processBatchWithRetry(context.Background(), slog.Default(), mockClient, batch))
			second := <-normalizedCh

			assert.Equal(t, orderedCanonicalTuples(first), orderedCanonicalTuples(second), "canonical tuple order drifted after fail-first/retry recovery")
			assertNoDuplicateCanonicalIDs(t, first)
			assertNoDuplicateCanonicalIDs(t, second)
			assert.Equal(t, tc.sequence, first.NewCursorSequence)
			assert.Equal(t, tc.sequence, second.NewCursorSequence)
			assert.Equal(t, 4, decodeAttempts)
		})
	}
}

func TestProcessBatchWithRetry_TerminalDecodeFailure_NoRetryAcrossMandatoryChains(t *testing.T) {
	testCases := []struct {
		name      string
		chain     model.Chain
		network   model.Network
		address   string
		signature string
		sequence  int64
	}{
		{
			name:      "solana-devnet",
			chain:     model.ChainSolana,
			network:   model.NetworkDevnet,
			address:   "addr-retry-solana",
			signature: "sig-sol-terminal-1",
			sequence:  101,
		},
		{
			name:      "base-sepolia",
			chain:     model.ChainBase,
			network:   model.NetworkSepolia,
			address:   "0x1111111111111111111111111111111111111111",
			signature: "0xbase-terminal-1",
			sequence:  202,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
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
				Chain:             tc.chain,
				Network:           tc.network,
				Address:           tc.address,
				RawTransactions:   []json.RawMessage{json.RawMessage(`{"tx":"terminal"}`)},
				Signatures:        []event.SignatureInfo{{Hash: tc.signature, Sequence: tc.sequence}},
				NewCursorValue:    strPtr(tc.signature),
				NewCursorSequence: tc.sequence,
			}

			decodeAttempts := 0
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, *sidecarv1.DecodeSolanaTransactionBatchRequest, ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					decodeAttempts++
					return nil, retry.Terminal(errors.New("decode schema mismatch"))
				}).
				Times(1)

			err := n.processBatchWithRetry(context.Background(), slog.Default(), mockClient, batch)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "terminal_failure stage=normalizer.decode_batch")
			assert.Equal(t, 1, decodeAttempts)
			assert.Equal(t, 0, sleepCalls)
		})
	}
}

func TestProcessBatchWithRetry_TransientDecodeFailureExhausted_StageDiagnostic(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	n := &Normalizer{
		sidecarTimeout:   30 * time.Second,
		normalizedCh:     make(chan event.NormalizedBatch, 1),
		logger:           slog.Default(),
		retryMaxAttempts: 2,
		retryDelayStart:  0,
		retryDelayMax:    0,
		sleepFn:          func(context.Context, time.Duration) error { return nil },
	}

	batch := event.RawBatch{
		Chain:             model.ChainSolana,
		Network:           model.NetworkDevnet,
		Address:           "addr1",
		RawTransactions:   []json.RawMessage{json.RawMessage(`{"tx":"retry"}`)},
		Signatures:        []event.SignatureInfo{{Hash: "sig-retry", Sequence: 1}},
		NewCursorValue:    strPtr("sig-retry"),
		NewCursorSequence: 1,
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, retry.Transient(errors.New("sidecar unavailable"))).
		Times(2)

	err := n.processBatchWithRetry(context.Background(), slog.Default(), mockClient, batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transient_recovery_exhausted stage=normalizer.decode_batch")
}

func TestWorker_PanicsOnProcessBatchError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	rawBatchCh := make(chan event.RawBatch, 1)
	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := &Normalizer{
		sidecarTimeout: 30_000_000_000,
		rawBatchCh:     rawBatchCh,
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	rawBatchCh <- event.RawBatch{
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
	close(rawBatchCh)

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("sidecar unavailable"))

	require.Panics(t, func() {
		n.worker(context.Background(), 0, mockClient)
	})
}

func TestProcessBatch_RawSignatureLengthMismatch(t *testing.T) {
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
			{Hash: "sig2", Sequence: 101},
		},
	}

	err := n.processBatch(context.Background(), slog.Default(), mockClient, batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "length mismatch")
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
