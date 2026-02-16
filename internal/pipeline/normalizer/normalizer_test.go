package normalizer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func TestProcessBatch_BoundaryPartitionVarianceEquivalentRangesAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name      string
		chain     model.Chain
		network   model.Network
		address   string
		signature []event.SignatureInfo
		buildTx   func(sig event.SignatureInfo, address string) *sidecarv1.TransactionResult
	}

	buildTransfer := func(sig event.SignatureInfo, address string, contract string) *sidecarv1.TransactionResult {
		return &sidecarv1.TransactionResult{
			TxHash:      sig.Hash,
			BlockCursor: sig.Sequence,
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
					Metadata:              map[string]string{"event_path": "log:7"},
				},
			},
		}
	}

	testCases := []testCase{
		{
			name:    "solana-devnet",
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			address: "sol-partition-addr",
			signature: []event.SignatureInfo{
				{Hash: "sol-partition-1", Sequence: 100},
				{Hash: "sol-partition-2", Sequence: 101},
				{Hash: "sol-partition-3", Sequence: 102},
			},
			buildTx: func(sig event.SignatureInfo, address string) *sidecarv1.TransactionResult {
				return buildTransfer(sig, address, "SOL")
			},
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			signature: []event.SignatureInfo{
				{Hash: "0xaaa", Sequence: 200},
				{Hash: "0xbbb", Sequence: 201},
				{Hash: "0xccc", Sequence: 202},
			},
			buildTx: func(sig event.SignatureInfo, address string) *sidecarv1.TransactionResult {
				return buildTransfer(sig, address, "ETH")
			},
		},
	}

	assertNoDuplicateCanonicalIDs := func(t *testing.T, tuples []tupleSignature) {
		t.Helper()
		seen := make(map[string]struct{}, len(tuples))
		for _, tuple := range tuples {
			_, exists := seen[tuple.EventID]
			require.False(t, exists, "duplicate canonical event id found: %s", tuple.EventID)
			seen[tuple.EventID] = struct{}{}
		}
	}

	runStrategy := func(t *testing.T, tc testCase, partitions [][]event.SignatureInfo) []tupleSignature {
		t.Helper()

		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)

		normalizedCh := make(chan event.NormalizedBatch, len(partitions))
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		for _, sigs := range partitions {
			expectedSigs := append([]event.SignatureInfo(nil), sigs...)
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					require.Len(t, req.GetTransactions(), len(expectedSigs))
					results := make([]*sidecarv1.TransactionResult, 0, len(expectedSigs))
					for idx, sig := range expectedSigs {
						assert.Equal(t, sig.Hash, req.GetTransactions()[idx].GetSignature())
						results = append(results, tc.buildTx(sig, tc.address))
					}
					return &sidecarv1.DecodeSolanaTransactionBatchResponse{Results: results}, nil
				})
		}

		allTuples := make([]tupleSignature, 0, 12)
		prevSequence := int64(0)
		for _, sigs := range partitions {
			rawTxs := make([]json.RawMessage, len(sigs))
			for i := range rawTxs {
				rawTxs[i] = json.RawMessage(`{"tx":"partition-variance"}`)
			}
			newCursor := sigs[len(sigs)-1].Hash
			batch := event.RawBatch{
				Chain:                  tc.chain,
				Network:                tc.network,
				Address:                tc.address,
				PreviousCursorSequence: prevSequence,
				RawTransactions:        rawTxs,
				Signatures:             sigs,
				NewCursorValue:         &newCursor,
				NewCursorSequence:      sigs[len(sigs)-1].Sequence,
			}

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			normalized := <-normalizedCh
			allTuples = append(allTuples, orderedCanonicalTuples(normalized)...)
			prevSequence = normalized.NewCursorSequence
		}

		return allTuples
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			strategyA := runStrategy(t, tc, [][]event.SignatureInfo{
				{tc.signature[0], tc.signature[1]},
				{tc.signature[2]},
			})
			strategyB := runStrategy(t, tc, [][]event.SignatureInfo{
				{tc.signature[0]},
				{tc.signature[1], tc.signature[2]},
			})

			assert.Equal(t, strategyA, strategyB, "canonical tuple ordering changed across deterministic partition strategies")
			assertNoDuplicateCanonicalIDs(t, strategyA)
			assertNoDuplicateCanonicalIDs(t, strategyB)

			mustContain := tc.signature[1].Hash
			foundA := false
			foundB := false
			for _, tuple := range strategyA {
				if tuple.TxHash == mustContain {
					foundA = true
					break
				}
			}
			for _, tuple := range strategyB {
				if tuple.TxHash == mustContain {
					foundB = true
					break
				}
			}
			assert.True(t, foundA, "missing boundary event in partition strategy A")
			assert.True(t, foundB, "missing boundary event in partition strategy B")
		})
	}
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

func TestProcessBatch_BaseSepoliaFeeDecomposition_AvailabilityOrderConverges(t *testing.T) {
	buildResult := func(partialFirst bool) *sidecarv1.TransactionResult {
		partial := &sidecarv1.BalanceEventInfo{
			OuterInstructionIndex: 0,
			InnerInstructionIndex: -1,
			EventCategory:         string(model.EventCategoryTransfer),
			EventAction:           "erc20_transfer_partial",
			ProgramId:             "0xabc",
			Address:               "0xaaaa1111111111111111111111111111111111111111",
			ContractAddress:       "ETH",
			Delta:                 "-1000",
			CounterpartyAddress:   "0xbbbb2222222222222222222222222222222222222222",
			TokenSymbol:           "ETH",
			TokenName:             "Ether",
			TokenDecimals:         18,
			TokenType:             string(model.TokenTypeNative),
			Metadata: map[string]string{
				"event_path":       "log:10",
				"fee_execution_l2": "1000",
			},
		}
		recovered := &sidecarv1.BalanceEventInfo{
			OuterInstructionIndex: 1,
			InnerInstructionIndex: -1,
			EventCategory:         string(model.EventCategoryTransfer),
			EventAction:           "erc20_transfer_recovered",
			ProgramId:             "0xabc",
			Address:               "0xaaaa1111111111111111111111111111111111111111",
			ContractAddress:       "ETH",
			Delta:                 "0",
			CounterpartyAddress:   "0xcccc3333333333333333333333333333333333333333",
			TokenSymbol:           "ETH",
			TokenName:             "Ether",
			TokenDecimals:         18,
			TokenType:             string(model.TokenTypeNative),
			Metadata: map[string]string{
				"fee_execution_l2": "600",
				"fee_data_l1":      "400",
			},
		}

		events := []*sidecarv1.BalanceEventInfo{partial, recovered}
		if !partialFirst {
			events = []*sidecarv1.BalanceEventInfo{recovered, partial}
		}

		return &sidecarv1.TransactionResult{
			TxHash:        "0xBEEF1001",
			BlockCursor:   1001,
			BlockTime:     1700000500,
			Status:        "SUCCESS",
			FeeAmount:     "1000",
			FeePayer:      "0x1111111111111111111111111111111111111111",
			BalanceEvents: events,
		}
	}

	runOnce := func(result *sidecarv1.TransactionResult) event.NormalizedBatch {
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
			Address: "0x1111111111111111111111111111111111111111",
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"base-fee-flap-order"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: "0xBEEF1001", Sequence: 1001},
			},
		}

		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{result},
			}, nil).
			Times(1)

		require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
		return <-normalizedCh
	}

	assertNoDuplicateCanonicalIDs := func(batch event.NormalizedBatch) {
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

	assertExpectedFeeSplit := func(batch event.NormalizedBatch) {
		require.Len(t, batch.Transactions, 1)
		tx := batch.Transactions[0]

		execEvents := fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeExecutionL2)
		dataEvents := fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeDataL1)
		require.Len(t, execEvents, 1)
		require.Len(t, dataEvents, 1)

		assert.Equal(t, "-600", execEvents[0].Delta)
		assert.Equal(t, "-400", dataEvents[0].Delta)

		execChainData := map[string]string{}
		require.NoError(t, json.Unmarshal(execEvents[0].ChainData, &execChainData))
		assert.Empty(t, execChainData["fee_total_mismatch"])
		assert.Empty(t, execChainData["data_fee_l1_unavailable"])
	}

	partialFirst := runOnce(buildResult(true))
	recoveredFirst := runOnce(buildResult(false))

	assert.Equal(t, orderedCanonicalTuples(recoveredFirst), orderedCanonicalTuples(partialFirst))
	assertNoDuplicateCanonicalIDs(partialFirst)
	assertNoDuplicateCanonicalIDs(recoveredFirst)
	assertExpectedFeeSplit(partialFirst)
	assertExpectedFeeSplit(recoveredFirst)
}

func TestProcessBatch_BaseSepoliaFeeDecomposition_AvailabilityFlapReplayPermutationsConverge(t *testing.T) {
	const watchedAddress = "0x1111111111111111111111111111111111111111"
	const signature = "0xBEEF2201"
	const sequence = int64(2201)
	const feePath = "log:42"

	buildResult := func(txHash string, metadata map[string]string) *sidecarv1.TransactionResult {
		return &sidecarv1.TransactionResult{
			TxHash:      txHash,
			BlockCursor: sequence,
			BlockTime:   1700000900,
			Status:      "SUCCESS",
			FeeAmount:   "1000",
			FeePayer:    watchedAddress,
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					OuterInstructionIndex: 0,
					InnerInstructionIndex: -1,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "availability_probe",
					ProgramId:             "0xabc",
					Address:               watchedAddress,
					ContractAddress:       "ETH",
					Delta:                 "-1",
					CounterpartyAddress:   "0x2222222222222222222222222222222222222222",
					TokenSymbol:           "ETH",
					TokenName:             "Ether",
					TokenDecimals:         18,
					TokenType:             string(model.TokenTypeNative),
					Metadata:              metadata,
				},
			},
		}
	}
	buildFull := func(txHash string) *sidecarv1.TransactionResult {
		return buildResult(txHash, map[string]string{
			"event_path": feePath,
		})
	}
	buildPartial := func(txHash string) *sidecarv1.TransactionResult {
		return buildResult(txHash, map[string]string{
			"event_path":       feePath,
			"fee_execution_l2": "1000",
		})
	}
	buildRecoveredNoPath := func(txHash string) *sidecarv1.TransactionResult {
		return buildResult(txHash, map[string]string{
			"fee_execution_l2": "600",
			"fee_data_l1":      "400",
		})
	}
	buildComplete := func(txHash string) *sidecarv1.TransactionResult {
		return buildResult(txHash, map[string]string{
			"event_path":       feePath,
			"fee_execution_l2": "600",
			"fee_data_l1":      "400",
		})
	}

	newBatch := func() event.RawBatch {
		return event.RawBatch{
			Chain:                  model.ChainBase,
			Network:                model.NetworkSepolia,
			Address:                watchedAddress,
			PreviousCursorValue:    strPtr("0xBEEF2200"),
			PreviousCursorSequence: sequence - 1,
			NewCursorValue:         strPtr(signature),
			NewCursorSequence:      sequence,
			RawTransactions: []json.RawMessage{
				json.RawMessage(`{"tx":"fee-availability-flap"}`),
			},
			Signatures: []event.SignatureInfo{
				{Hash: signature, Sequence: sequence},
			},
		}
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

	feePair := func(t *testing.T, batch event.NormalizedBatch) (event.NormalizedBalanceEvent, event.NormalizedBalanceEvent) {
		t.Helper()
		require.Len(t, batch.Transactions, 1)
		tx := batch.Transactions[0]
		execEvents := fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeExecutionL2)
		dataEvents := fixtureBaseFeeEventsByCategory(tx.BalanceEvents, model.EventCategoryFeeDataL1)
		require.Len(t, execEvents, 1)
		require.Len(t, dataEvents, 1)
		return execEvents[0], dataEvents[0]
	}

	assertConvergedFeePair := func(t *testing.T, batch event.NormalizedBatch, baselineExec, baselineData event.NormalizedBalanceEvent) {
		t.Helper()
		execEvent, dataEvent := feePair(t, batch)
		assert.Equal(t, baselineExec.EventID, execEvent.EventID)
		assert.Equal(t, baselineData.EventID, dataEvent.EventID)
		assert.Equal(t, baselineExec.Delta, execEvent.Delta)
		assert.Equal(t, baselineData.Delta, dataEvent.Delta)
		assert.Equal(t, baselineExec.EventPath, execEvent.EventPath)
		assert.Equal(t, baselineData.EventPath, dataEvent.EventPath)

		execChainData := map[string]string{}
		require.NoError(t, json.Unmarshal(execEvent.ChainData, &execChainData))
		assert.Empty(t, execChainData["data_fee_l1_unavailable"])
	}

	runSingle := func(results []*sidecarv1.TransactionResult) event.NormalizedBatch {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)
		normalizedCh := make(chan event.NormalizedBatch, 1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: results,
			}, nil).
			Times(1)

		batch := newBatch()
		require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
		return <-normalizedCh
	}

	baseline := runSingle([]*sidecarv1.TransactionResult{
		buildComplete("0xbeef2201"),
	})
	baselineExec, baselineData := feePair(t, baseline)
	assert.Equal(t, "-600", baselineExec.Delta)
	assert.Equal(t, "-400", baselineData.Delta)
	assert.Equal(t, feePath, baselineExec.EventPath)
	assert.Equal(t, feePath, baselineData.EventPath)

	permutations := []struct {
		name    string
		results []*sidecarv1.TransactionResult
	}{
		{
			name: "full_then_recovered",
			results: []*sidecarv1.TransactionResult{
				buildFull("BEEF2201"),
				buildRecoveredNoPath("0xbeef2201"),
			},
		},
		{
			name: "partial_then_recovered",
			results: []*sidecarv1.TransactionResult{
				buildPartial("0XBEEF2201"),
				buildRecoveredNoPath("0xbeef2201"),
			},
		},
		{
			name: "recovered_then_full",
			results: []*sidecarv1.TransactionResult{
				buildRecoveredNoPath("0xbeef2201"),
				buildFull("BEEF2201"),
			},
		},
		{
			name: "recovered_then_partial",
			results: []*sidecarv1.TransactionResult{
				buildRecoveredNoPath("0xbeef2201"),
				buildPartial("BEEF2201"),
			},
		},
		{
			name: "full_partial_recovered",
			results: []*sidecarv1.TransactionResult{
				buildFull("BEEF2201"),
				buildPartial("0xBEEF2201"),
				buildRecoveredNoPath("beef2201"),
			},
		},
	}

	for _, tc := range permutations {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			normalized := runSingle(tc.results)
			assertNoDuplicateCanonicalIDs(t, normalized)
			assertConvergedFeePair(t, normalized, baselineExec, baselineData)
		})
	}

	runReplay := func(first, second *sidecarv1.TransactionResult) (event.NormalizedBatch, event.NormalizedBatch) {
		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)
		normalizedCh := make(chan event.NormalizedBatch, 2)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		call := 0
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(2).
			DoAndReturn(func(context.Context, *sidecarv1.DecodeSolanaTransactionBatchRequest, ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
				call++
				if call == 1 {
					return &sidecarv1.DecodeSolanaTransactionBatchResponse{
						Results: []*sidecarv1.TransactionResult{first},
					}, nil
				}
				return &sidecarv1.DecodeSolanaTransactionBatchResponse{
					Results: []*sidecarv1.TransactionResult{second},
				}, nil
			})

		firstBatch := newBatch()
		require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, firstBatch))
		degraded := <-normalizedCh

		replayBatch := newBatch()
		replayBatch.PreviousCursorValue = degraded.NewCursorValue
		replayBatch.PreviousCursorSequence = degraded.NewCursorSequence
		replayBatch.NewCursorValue = degraded.NewCursorValue
		replayBatch.NewCursorSequence = degraded.NewCursorSequence

		require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, replayBatch))
		recovered := <-normalizedCh
		return degraded, recovered
	}

	replayCases := []struct {
		name  string
		first *sidecarv1.TransactionResult
	}{
		{name: "full_then_recovered_replay", first: buildFull("BEEF2201")},
		{name: "partial_then_recovered_replay", first: buildPartial("0xBEEF2201")},
	}

	for _, tc := range replayCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			degraded, recovered := runReplay(tc.first, buildRecoveredNoPath("0xbeef2201"))
			assertNoDuplicateCanonicalIDs(t, degraded)
			assertNoDuplicateCanonicalIDs(t, recovered)

			require.Len(t, degraded.Transactions, 1)
			degradedTx := degraded.Transactions[0]
			degradedExec := fixtureBaseFeeEventsByCategory(degradedTx.BalanceEvents, model.EventCategoryFeeExecutionL2)
			degradedData := fixtureBaseFeeEventsByCategory(degradedTx.BalanceEvents, model.EventCategoryFeeDataL1)
			require.Len(t, degradedExec, 1)
			require.Len(t, degradedData, 0)

			degradedChainData := map[string]string{}
			require.NoError(t, json.Unmarshal(degradedExec[0].ChainData, &degradedChainData))
			assert.Equal(t, "true", degradedChainData["data_fee_l1_unavailable"])

			assertConvergedFeePair(t, recovered, baselineExec, baselineData)
			require.NotNil(t, degraded.NewCursorValue)
			require.NotNil(t, recovered.NewCursorValue)
			assert.Equal(t, *degraded.NewCursorValue, *recovered.NewCursorValue)
			assert.GreaterOrEqual(t, recovered.NewCursorSequence, degraded.NewCursorSequence)
		})
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

func TestProcessBatch_MixedTerminalDecodeFailures_DeterministicContinuationAcrossMandatoryChains(t *testing.T) {
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
		errorsA        []*sidecarv1.DecodeError
		errorsB        []*sidecarv1.DecodeError
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
			address: "addr-owner-terminal-mixed",
			signatures: []event.SignatureInfo{
				{Hash: "sol-ok-1", Sequence: 801},
				{Hash: "sol-fail-parse", Sequence: 802},
				{Hash: "sol-fail-schema", Sequence: 803},
				{Hash: "sol-ok-2", Sequence: 804},
			},
			expectedTxs:    []string{"sol-ok-1", "sol-ok-2"},
			expectedCursor: "sol-ok-2",
			responseA: []*sidecarv1.TransactionResult{
				buildTransfer("sol-ok-1", 801, "addr-owner-terminal-mixed", "SOL"),
				buildTransfer("sol-ok-2", 804, "addr-owner-terminal-mixed", "SOL"),
			},
			responseB: []*sidecarv1.TransactionResult{
				buildTransfer("sol-ok-2", 804, "addr-owner-terminal-mixed", "SOL"),
				buildTransfer("sol-ok-1", 801, "addr-owner-terminal-mixed", "SOL"),
			},
			errorsA: []*sidecarv1.DecodeError{
				{Signature: "sol-fail-parse", Error: "parse error"},
				{Signature: "sol-fail-schema", Error: "decode schema mismatch"},
			},
			errorsB: []*sidecarv1.DecodeError{
				{Signature: "sol-fail-schema", Error: "decode schema mismatch"},
				{Signature: "sol-fail-parse", Error: "parse error"},
			},
		},
		{
			name:    "base-sepolia",
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			address: "0x1111111111111111111111111111111111111111",
			signatures: []event.SignatureInfo{
				{Hash: "0xabc801", Sequence: 901},
				{Hash: "0xabc802", Sequence: 902},
				{Hash: "0xabc803", Sequence: 903},
				{Hash: "0xabc804", Sequence: 904},
			},
			expectedTxs:    []string{"0xabc801", "0xabc804"},
			expectedCursor: "0xabc804",
			responseA: []*sidecarv1.TransactionResult{
				buildTransfer("0xabc804", 904, "0x1111111111111111111111111111111111111111", "ETH"),
				buildTransfer("0xabc801", 901, "0x1111111111111111111111111111111111111111", "ETH"),
			},
			responseB: []*sidecarv1.TransactionResult{
				buildTransfer("0xabc801", 901, "0x1111111111111111111111111111111111111111", "ETH"),
				buildTransfer("0xabc804", 904, "0x1111111111111111111111111111111111111111", "ETH"),
			},
			errorsA: []*sidecarv1.DecodeError{
				{Signature: "0xabc802", Error: "parse error"},
				{Signature: "0xabc803", Error: "decode schema mismatch"},
			},
			errorsB: []*sidecarv1.DecodeError{
				{Signature: "0xabc803", Error: "decode schema mismatch"},
				{Signature: "0xabc802", Error: "parse error"},
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
				rawTxs[i] = json.RawMessage(`{"tx":"mixed-terminal"}`)
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
						return &sidecarv1.DecodeSolanaTransactionBatchResponse{
							Results: tc.responseA,
							Errors:  tc.errorsA,
						}, nil
					}
					return &sidecarv1.DecodeSolanaTransactionBatchResponse{
						Results: tc.responseB,
						Errors:  tc.errorsB,
					}, nil
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

func TestProcessBatch_DeferredRecoveryBackfillPermutationsConvergeAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name             string
		chain            model.Chain
		network          model.Network
		address          string
		firstSig         string
		secondSig        string
		thirdSig         string
		recoveredSecond  string
		alreadyDecoded   string
		orphanSig        string
		previousSig      string
		previousSequence int64
		buildResult      func(*testing.T, string, int64) *sidecarv1.TransactionResult
	}

	testCases := []testCase{
		{
			name:             "solana-devnet",
			chain:            model.ChainSolana,
			network:          model.NetworkDevnet,
			address:          "addr-sidecar-recovery-sol",
			firstSig:         "sol-recovery-1201",
			secondSig:        "sol-recovery-1202",
			thirdSig:         "sol-recovery-1203",
			recoveredSecond:  "sol-recovery-1202",
			alreadyDecoded:   "sol-recovery-1203",
			orphanSig:        "aaa-sol-prior-range-0999",
			previousSig:      "sol-prior-range-1200",
			previousSequence: 1200,
			buildResult: func(t *testing.T, signature string, sequence int64) *sidecarv1.TransactionResult {
				result := loadSolanaFixture(t, "solana_cpi_ownership_scoped.json")
				result.TxHash = signature
				result.BlockCursor = sequence
				return result
			},
		},
		{
			name:             "base-sepolia",
			chain:            model.ChainBase,
			network:          model.NetworkSepolia,
			address:          "0x1111111111111111111111111111111111111111",
			firstSig:         "0xAbCd1201",
			secondSig:        "0xAbCd1202",
			thirdSig:         "0xAbCd1203",
			recoveredSecond:  "ABCD1202",
			alreadyDecoded:   "abcd1203",
			orphanSig:        "0x00000000000000000000000000000000000000000000000000000000000000aa",
			previousSig:      "0xAbCd1200",
			previousSequence: 2200,
			buildResult: func(t *testing.T, signature string, sequence int64) *sidecarv1.TransactionResult {
				result := loadBaseFeeFixture(t, "base_fee_decomposition_complete.json")
				result.TxHash = signature
				result.BlockCursor = sequence
				return result
			},
		},
	}

	canonicalEventIDSet := func(batches ...event.NormalizedBatch) map[string]struct{} {
		out := make(map[string]struct{})
		for _, batch := range batches {
			for _, tx := range batch.Transactions {
				for _, be := range tx.BalanceEvents {
					out[be.EventID] = struct{}{}
				}
			}
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
			firstSeq := tc.previousSequence + 1
			secondSeq := tc.previousSequence + 2
			thirdSeq := tc.previousSequence + 3

			canonicalFirst := canonicalSignatureIdentity(tc.chain, tc.firstSig)
			canonicalSecond := canonicalSignatureIdentity(tc.chain, tc.secondSig)
			canonicalThird := canonicalSignatureIdentity(tc.chain, tc.thirdSig)
			require.NotEmpty(t, canonicalFirst)
			require.NotEmpty(t, canonicalSecond)
			require.NotEmpty(t, canonicalThird)

			baseBatch := event.RawBatch{
				Chain:                  tc.chain,
				Network:                tc.network,
				Address:                tc.address,
				PreviousCursorValue:    strPtr(tc.previousSig),
				PreviousCursorSequence: tc.previousSequence,
				NewCursorValue:         strPtr(tc.thirdSig),
				NewCursorSequence:      thirdSeq,
				RawTransactions: []json.RawMessage{
					json.RawMessage(`{"tx":"one"}`),
					json.RawMessage(`{"tx":"two"}`),
					json.RawMessage(`{"tx":"three"}`),
				},
				Signatures: []event.SignatureInfo{
					{Hash: tc.firstSig, Sequence: firstSeq},
					{Hash: tc.secondSig, Sequence: secondSeq},
					{Hash: tc.thirdSig, Sequence: thirdSeq},
				},
			}

			runOnce := func(resp *sidecarv1.DecodeSolanaTransactionBatchResponse, runBatch event.RawBatch) event.NormalizedBatch {
				ctrl := gomock.NewController(t)
				mockClient := mocks.NewMockChainDecoderClient(ctrl)
				normalizedCh := make(chan event.NormalizedBatch, 1)
				n := &Normalizer{
					sidecarTimeout: 30 * time.Second,
					normalizedCh:   normalizedCh,
					logger:         slog.Default(),
				}

				mockClient.EXPECT().
					DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(resp, nil).
					Times(1)

				require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, runBatch))
				return <-normalizedCh
			}

			baseline := runOnce(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					tc.buildResult(t, tc.thirdSig, thirdSeq),
					tc.buildResult(t, tc.firstSig, firstSeq),
					tc.buildResult(t, tc.secondSig, secondSeq),
				},
			}, baseBatch)

			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)
			normalizedCh := make(chan event.NormalizedBatch, 2)
			n := &Normalizer{
				sidecarTimeout: 30 * time.Second,
				normalizedCh:   normalizedCh,
				logger:         slog.Default(),
			}

			call := 0
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(2).
				DoAndReturn(func(context.Context, *sidecarv1.DecodeSolanaTransactionBatchRequest, ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					call++
					if call == 1 {
						return &sidecarv1.DecodeSolanaTransactionBatchResponse{
							Results: []*sidecarv1.TransactionResult{
								tc.buildResult(t, tc.firstSig, firstSeq),
								tc.buildResult(t, tc.thirdSig, thirdSeq),
							},
							Errors: []*sidecarv1.DecodeError{
								{Signature: tc.secondSig, Error: "decode schema mismatch"},
							},
						}, nil
					}
					return &sidecarv1.DecodeSolanaTransactionBatchResponse{
						Results: []*sidecarv1.TransactionResult{
							tc.buildResult(t, tc.orphanSig, tc.previousSequence-1),
							tc.buildResult(t, tc.recoveredSecond, secondSeq),
							tc.buildResult(t, tc.alreadyDecoded, thirdSeq),
						},
					}, nil
				})

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, baseBatch))
			degraded := <-normalizedCh
			require.NotNil(t, degraded.NewCursorValue)

			recoveryBatch := baseBatch
			recoveryBatch.PreviousCursorValue = degraded.NewCursorValue
			recoveryBatch.PreviousCursorSequence = degraded.NewCursorSequence
			recoveryBatch.NewCursorValue = degraded.NewCursorValue
			recoveryBatch.NewCursorSequence = degraded.NewCursorSequence

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, recoveryBatch))
			recovered := <-normalizedCh

			assertNoDuplicateCanonicalIDs(t, baseline)
			assertNoDuplicateCanonicalIDs(t, degraded)
			assertNoDuplicateCanonicalIDs(t, recovered)
			assert.Equal(t, canonicalEventIDSet(baseline), canonicalEventIDSet(degraded, recovered), "degradation->recovery output must reconcile to fully decodable baseline")

			recoveredTxHashes := txHashes(recovered)
			assert.Contains(t, recoveredTxHashes, canonicalSecond)
			assert.Contains(t, recoveredTxHashes, canonicalThird)
			assert.NotContains(t, recoveredTxHashes, canonicalSignatureIdentity(tc.chain, tc.orphanSig))

			require.NotNil(t, recovered.NewCursorValue)
			assert.Equal(t, degraded.NewCursorSequence, recovered.NewCursorSequence)
			assert.Equal(t, *degraded.NewCursorValue, *recovered.NewCursorValue)
			assert.GreaterOrEqual(t, recovered.NewCursorSequence, recoveryBatch.PreviousCursorSequence)

			ctrlRetry := gomock.NewController(t)
			retryClient := mocks.NewMockChainDecoderClient(ctrlRetry)
			retryCh := make(chan event.NormalizedBatch, 1)
			nRetry := &Normalizer{
				sidecarTimeout:   30 * time.Second,
				normalizedCh:     retryCh,
				logger:           slog.Default(),
				retryMaxAttempts: 2,
				retryDelayStart:  0,
				retryDelayMax:    0,
				sleepFn:          func(context.Context, time.Duration) error { return nil },
			}
			retryAttempts := 0
			retryClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(2).
				DoAndReturn(func(context.Context, *sidecarv1.DecodeSolanaTransactionBatchRequest, ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					retryAttempts++
					if retryAttempts == 1 {
						return nil, retry.Transient(errors.New("sidecar unavailable"))
					}
					return &sidecarv1.DecodeSolanaTransactionBatchResponse{
						Results: []*sidecarv1.TransactionResult{
							tc.buildResult(t, tc.firstSig, firstSeq),
							tc.buildResult(t, tc.secondSig, secondSeq),
							tc.buildResult(t, tc.thirdSig, thirdSeq),
						},
					}, nil
				})
			require.NoError(t, nRetry.processBatchWithRetry(context.Background(), slog.Default(), retryClient, baseBatch))
			require.Equal(t, 2, retryAttempts)
			retryRecovered := <-retryCh
			assert.Equal(t, orderedCanonicalTuples(baseline), orderedCanonicalTuples(retryRecovered), "temporary unavailable recovery must converge to fully decodable baseline")
		})
	}
}

func TestProcessBatchWithRetry_TransientDecodeErrorsInResponse_BlockCursorAdvanceAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name          string
		chain         model.Chain
		network       model.Network
		address       string
		firstSig      string
		secondSig     string
		firstSequence int64
		buildResult   func(*testing.T, string, int64) *sidecarv1.TransactionResult
	}

	testCases := []testCase{
		{
			name:          "solana-devnet",
			chain:         model.ChainSolana,
			network:       model.NetworkDevnet,
			address:       "addr-retry-solana-response",
			firstSig:      "sol-response-transient-1",
			secondSig:     "sol-response-transient-2",
			firstSequence: 1201,
			buildResult: func(t *testing.T, signature string, sequence int64) *sidecarv1.TransactionResult {
				result := loadSolanaFixture(t, "solana_cpi_ownership_scoped.json")
				result.TxHash = signature
				result.BlockCursor = sequence
				return result
			},
		},
		{
			name:          "base-sepolia",
			chain:         model.ChainBase,
			network:       model.NetworkSepolia,
			address:       "0x1111111111111111111111111111111111111111",
			firstSig:      "0x1201",
			secondSig:     "0x1202",
			firstSequence: 2201,
			buildResult: func(t *testing.T, signature string, sequence int64) *sidecarv1.TransactionResult {
				result := loadBaseFeeFixture(t, "base_fee_decomposition_complete.json")
				result.TxHash = signature
				result.BlockCursor = sequence
				return result
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

			secondSequence := tc.firstSequence + 1
			batch := event.RawBatch{
				Chain:           tc.chain,
				Network:         tc.network,
				Address:         tc.address,
				RawTransactions: []json.RawMessage{json.RawMessage(`{"tx":"first"}`), json.RawMessage(`{"tx":"second"}`)},
				Signatures: []event.SignatureInfo{
					{Hash: tc.firstSig, Sequence: tc.firstSequence},
					{Hash: tc.secondSig, Sequence: secondSequence},
				},
			}

			decodeAttempts := 0
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(2).
				DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					require.Len(t, req.GetTransactions(), 2)
					decodeAttempts++
					if decodeAttempts == 1 {
						return &sidecarv1.DecodeSolanaTransactionBatchResponse{
							Results: []*sidecarv1.TransactionResult{
								tc.buildResult(t, tc.firstSig, tc.firstSequence),
							},
							Errors: []*sidecarv1.DecodeError{
								{Signature: tc.secondSig, Error: "sidecar unavailable"},
							},
						}, nil
					}
					return &sidecarv1.DecodeSolanaTransactionBatchResponse{
						Results: []*sidecarv1.TransactionResult{
							tc.buildResult(t, tc.firstSig, tc.firstSequence),
							tc.buildResult(t, tc.secondSig, secondSequence),
						},
					}, nil
				})

			require.NoError(t, n.processBatchWithRetry(context.Background(), slog.Default(), mockClient, batch))
			require.Equal(t, 2, decodeAttempts)
			require.Equal(t, 1, len(normalizedCh), "transient decode-error attempt must not emit pre-retry normalized output")

			normalized := <-normalizedCh
			assert.Equal(t, []string{tc.firstSig, tc.secondSig}, txHashes(normalized))
			require.NotNil(t, normalized.NewCursorValue)
			assert.Equal(t, tc.secondSig, *normalized.NewCursorValue)
			assert.Equal(t, secondSequence, normalized.NewCursorSequence)
		})
	}
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

func TestProcessBatch_DecoderVersionPermutationsConvergeAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name           string
		chain          model.Chain
		network        model.Network
		address        string
		signature      string
		expectedTxHash string
		buildLegacy    func() *sidecarv1.TransactionResult
		buildUpgraded  func() *sidecarv1.TransactionResult
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

	testCases := []testCase{
		{
			name:           "solana-devnet",
			chain:          model.ChainSolana,
			network:        model.NetworkDevnet,
			address:        "sol-decoder-owner",
			signature:      "sol-decoder-transition-1",
			expectedTxHash: "sol-decoder-transition-1",
			buildLegacy: func() *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      "sol-decoder-transition-1",
					BlockCursor: 8101,
					FeeAmount:   "1000",
					FeePayer:    "sol-decoder-owner",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "legacy_transfer",
							ProgramId:             "11111111111111111111111111111111",
							Address:               "sol-decoder-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-10",
							CounterpartyAddress:   "sol-counterparty-1",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"decoder_version": "solana-decoder-v0",
							},
						},
					},
				}
			},
			buildUpgraded: func() *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      "sol-decoder-transition-1",
					BlockCursor: 8101,
					FeeAmount:   "1000",
					FeePayer:    "sol-decoder-owner",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 7,
							InnerInstructionIndex: 5,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "upgraded_transfer",
							ProgramId:             "11111111111111111111111111111111",
							Address:               "sol-decoder-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-10",
							CounterpartyAddress:   "sol-counterparty-1",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"event_path":      "outer:0|inner:-1",
								"decoder_version": "solana-decoder-v1",
								"schema_version":  "v2",
							},
						},
					},
				}
			},
		},
		{
			name:           "base-sepolia",
			chain:          model.ChainBase,
			network:        model.NetworkSepolia,
			address:        "0xABCD000000000000000000000000000000000001",
			signature:      "A0B1C2D4",
			expectedTxHash: "0xa0b1c2d4",
			buildLegacy: func() *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      "A0B1C2D4",
					BlockCursor: 9101,
					FeeAmount:   "1000",
					FeePayer:    "0xABCD000000000000000000000000000000000001",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "legacy_erc20_transfer",
							ProgramId:             "0xABCDEF0000000000000000000000000000000000",
							Address:               "0xABCD000000000000000000000000000000000001",
							ContractAddress:       "0xABCDEF0000000000000000000000000000000000",
							Delta:                 "-25",
							CounterpartyAddress:   "0xDCBA000000000000000000000000000000000001",
							TokenSymbol:           "",
							TokenName:             "",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"log_index":       "12",
								"decoder_version": "base-decoder-v0",
							},
						},
					},
				}
			},
			buildUpgraded: func() *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      "0xa0b1c2d4",
					BlockCursor: 9101,
					FeeAmount:   "1000",
					FeePayer:    "0xabcd000000000000000000000000000000000001",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 14,
							InnerInstructionIndex: 2,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "upgraded_erc20_transfer",
							ProgramId:             "0xabcdef0000000000000000000000000000000000",
							Address:               "0xabcd000000000000000000000000000000000001",
							ContractAddress:       "0xabcdef0000000000000000000000000000000000",
							Delta:                 "-25",
							CounterpartyAddress:   "0xdcba000000000000000000000000000000000001",
							TokenSymbol:           "",
							TokenName:             "",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"event_path":      "log:12",
								"base_event_path": "log:12",
								"log_index":       "12",
								"decoder_version": "base-decoder-v1",
								"schema_version":  "v2",
							},
						},
					},
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)

			normalizedCh := make(chan event.NormalizedBatch, 4)
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
					json.RawMessage(`{"tx":"decoder-transition"}`),
				},
				Signatures: []event.SignatureInfo{
					{Hash: tc.signature, Sequence: 101},
				},
			}

			responses := []*sidecarv1.DecodeSolanaTransactionBatchResponse{
				{Results: []*sidecarv1.TransactionResult{tc.buildLegacy()}},
				{Results: []*sidecarv1.TransactionResult{tc.buildUpgraded()}},
				{Results: []*sidecarv1.TransactionResult{tc.buildLegacy(), tc.buildUpgraded()}},
				{Results: []*sidecarv1.TransactionResult{tc.buildUpgraded(), tc.buildLegacy()}},
			}

			call := 0
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(len(responses)).
				DoAndReturn(func(context.Context, *sidecarv1.DecodeSolanaTransactionBatchRequest, ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					resp := responses[call]
					call++
					return resp, nil
				})

			outputs := make([]event.NormalizedBatch, 0, len(responses))
			for range responses {
				require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
				outputs = append(outputs, <-normalizedCh)
			}

			legacyOnly := outputs[0]
			upgradedOnly := outputs[1]
			mixedLegacyFirst := outputs[2]
			mixedUpgradedFirst := outputs[3]

			require.Len(t, legacyOnly.Transactions, 1)
			require.Len(t, upgradedOnly.Transactions, 1)
			require.Len(t, mixedLegacyFirst.Transactions, 1)
			require.Len(t, mixedUpgradedFirst.Transactions, 1)
			assert.Equal(t, tc.expectedTxHash, legacyOnly.Transactions[0].TxHash)
			assert.Equal(t, tc.expectedTxHash, upgradedOnly.Transactions[0].TxHash)
			assert.Equal(t, tc.expectedTxHash, mixedLegacyFirst.Transactions[0].TxHash)
			assert.Equal(t, tc.expectedTxHash, mixedUpgradedFirst.Transactions[0].TxHash)

			assert.Equal(t, orderedCanonicalTuples(legacyOnly), orderedCanonicalTuples(upgradedOnly))
			assert.Equal(t, orderedCanonicalTuples(upgradedOnly), orderedCanonicalTuples(mixedLegacyFirst))
			assert.Equal(t, orderedCanonicalTuples(mixedLegacyFirst), orderedCanonicalTuples(mixedUpgradedFirst))

			assertNoDuplicateCanonicalIDs(t, legacyOnly)
			assertNoDuplicateCanonicalIDs(t, upgradedOnly)
			assertNoDuplicateCanonicalIDs(t, mixedLegacyFirst)
			assertNoDuplicateCanonicalIDs(t, mixedUpgradedFirst)
		})
	}
}

func TestProcessBatch_DecoderVersionTransitionReplayResumeIdempotentAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name            string
		chain           model.Chain
		network         model.Network
		address         string
		firstSig        string
		secondSig       string
		firstSeq        int64
		secondSeq       int64
		expectedCursor  string
		buildLegacyTx   func(signature string, sequence int64) *sidecarv1.TransactionResult
		buildUpgradedTx func(signature string, sequence int64) *sidecarv1.TransactionResult
	}

	canonicalEventIDSet := func(batch event.NormalizedBatch) map[string]struct{} {
		out := make(map[string]struct{})
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				out[be.EventID] = struct{}{}
			}
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

	testCases := []testCase{
		{
			name:           "solana-devnet",
			chain:          model.ChainSolana,
			network:        model.NetworkDevnet,
			address:        "sol-resume-owner",
			firstSig:       "sol-resume-1",
			secondSig:      "sol-resume-2",
			firstSeq:       1201,
			secondSeq:      1202,
			expectedCursor: "sol-resume-2",
			buildLegacyTx: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "500",
					FeePayer:    "sol-resume-owner",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "legacy_transfer",
							ProgramId:             "11111111111111111111111111111111",
							Address:               "sol-resume-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-3",
							CounterpartyAddress:   "sol-resume-counterparty",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"decoder_version": "solana-decoder-v0",
							},
						},
					},
				}
			},
			buildUpgradedTx: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "500",
					FeePayer:    "sol-resume-owner",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 5,
							InnerInstructionIndex: 4,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "upgraded_transfer",
							ProgramId:             "11111111111111111111111111111111",
							Address:               "sol-resume-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-3",
							CounterpartyAddress:   "sol-resume-counterparty",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"event_path":      "outer:0|inner:-1",
								"decoder_version": "solana-decoder-v1",
							},
						},
					},
				}
			},
		},
		{
			name:           "base-sepolia",
			chain:          model.ChainBase,
			network:        model.NetworkSepolia,
			address:        "0xABCD0000000000000000000000000000000000AA",
			firstSig:       "ABCD1201",
			secondSig:      "ABCD1202",
			firstSeq:       2201,
			secondSeq:      2202,
			expectedCursor: "0xabcd1202",
			buildLegacyTx: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "750",
					FeePayer:    "0xABCD0000000000000000000000000000000000AA",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "legacy_erc20_transfer",
							ProgramId:             "0xABCDEF00000000000000000000000000000000AA",
							Address:               "0xABCD0000000000000000000000000000000000AA",
							ContractAddress:       "0xABCDEF00000000000000000000000000000000AA",
							Delta:                 "-7",
							CounterpartyAddress:   "0xDCBA0000000000000000000000000000000000AA",
							TokenSymbol:           "",
							TokenName:             "",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"log_index":       "33",
								"decoder_version": "base-decoder-v0",
							},
						},
					},
				}
			},
			buildUpgradedTx: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      "0x" + strings.ToLower(signature),
					BlockCursor: sequence,
					FeeAmount:   "750",
					FeePayer:    "0xabcd0000000000000000000000000000000000aa",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 20,
							InnerInstructionIndex: 3,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "upgraded_erc20_transfer",
							ProgramId:             "0xabcdef00000000000000000000000000000000aa",
							Address:               "0xabcd0000000000000000000000000000000000aa",
							ContractAddress:       "0xabcdef00000000000000000000000000000000aa",
							Delta:                 "-7",
							CounterpartyAddress:   "0xdcba0000000000000000000000000000000000aa",
							TokenSymbol:           "",
							TokenName:             "",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"event_path":      "log:33",
								"base_event_path": "log:33",
								"log_index":       "33",
								"decoder_version": "base-decoder-v1",
							},
						},
					},
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)
			normalizedCh := make(chan event.NormalizedBatch, 3)
			n := &Normalizer{
				sidecarTimeout: 30 * time.Second,
				normalizedCh:   normalizedCh,
				logger:         slog.Default(),
			}

			batch := event.RawBatch{
				Chain:                  tc.chain,
				Network:                tc.network,
				Address:                tc.address,
				PreviousCursorValue:    strPtr("prior"),
				PreviousCursorSequence: tc.firstSeq - 1,
				NewCursorValue:         strPtr(tc.secondSig),
				NewCursorSequence:      tc.secondSeq,
				RawTransactions: []json.RawMessage{
					json.RawMessage(`{"tx":"one"}`),
					json.RawMessage(`{"tx":"two"}`),
				},
				Signatures: []event.SignatureInfo{
					{Hash: tc.firstSig, Sequence: tc.firstSeq},
					{Hash: tc.secondSig, Sequence: tc.secondSeq},
				},
			}

			responses := []*sidecarv1.DecodeSolanaTransactionBatchResponse{
				{
					Results: []*sidecarv1.TransactionResult{
						tc.buildUpgradedTx(tc.firstSig, tc.firstSeq),
						tc.buildUpgradedTx(tc.secondSig, tc.secondSeq),
					},
				},
				{
					Results: []*sidecarv1.TransactionResult{
						tc.buildLegacyTx(tc.firstSig, tc.firstSeq),
						tc.buildUpgradedTx(tc.secondSig, tc.secondSeq),
					},
				},
				{
					Results: []*sidecarv1.TransactionResult{
						tc.buildUpgradedTx(tc.firstSig, tc.firstSeq),
						tc.buildLegacyTx(tc.secondSig, tc.secondSeq),
					},
				},
			}

			call := 0
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(len(responses)).
				DoAndReturn(func(context.Context, *sidecarv1.DecodeSolanaTransactionBatchRequest, ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					resp := responses[call]
					call++
					return resp, nil
				})

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			baseline := <-normalizedCh

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			firstReplay := <-normalizedCh

			replayBatch := batch
			replayBatch.PreviousCursorValue = firstReplay.NewCursorValue
			replayBatch.PreviousCursorSequence = firstReplay.NewCursorSequence
			replayBatch.NewCursorValue = firstReplay.NewCursorValue
			replayBatch.NewCursorSequence = firstReplay.NewCursorSequence

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, replayBatch))
			secondReplay := <-normalizedCh

			assert.Equal(t, orderedCanonicalTuples(baseline), orderedCanonicalTuples(firstReplay))
			assert.Equal(t, orderedCanonicalTuples(firstReplay), orderedCanonicalTuples(secondReplay))
			assert.Equal(t, canonicalEventIDSet(baseline), canonicalEventIDSet(firstReplay))
			assert.Equal(t, canonicalEventIDSet(firstReplay), canonicalEventIDSet(secondReplay))

			assertNoDuplicateCanonicalIDs(t, baseline)
			assertNoDuplicateCanonicalIDs(t, firstReplay)
			assertNoDuplicateCanonicalIDs(t, secondReplay)

			require.NotNil(t, firstReplay.NewCursorValue)
			require.NotNil(t, secondReplay.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *firstReplay.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *secondReplay.NewCursorValue)
			assert.GreaterOrEqual(t, firstReplay.NewCursorSequence, firstReplay.PreviousCursorSequence)
			assert.GreaterOrEqual(t, secondReplay.NewCursorSequence, secondReplay.PreviousCursorSequence)
			assert.GreaterOrEqual(t, secondReplay.NewCursorSequence, firstReplay.NewCursorSequence)
		})
	}
}

func TestProcessBatch_IncrementalDecodeCoverageConvergenceAndReplayAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name                   string
		chain                  model.Chain
		network                model.Network
		address                string
		signature              string
		sequence               int64
		expectedCursor         string
		buildSparseEquivalent  func(string, int64) *sidecarv1.TransactionResult
		buildEnrichedComplete  func(string, int64) *sidecarv1.TransactionResult
		buildSparsePartial     func(string, int64) *sidecarv1.TransactionResult
		buildEnrichedPartial   func(string, int64) *sidecarv1.TransactionResult
		expectedTransferEvents int
	}

	canonicalEventIDSet := func(batches ...event.NormalizedBatch) map[string]struct{} {
		out := make(map[string]struct{})
		for _, batch := range batches {
			for _, tx := range batch.Transactions {
				for _, be := range tx.BalanceEvents {
					out[be.EventID] = struct{}{}
				}
			}
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
	countCategory := func(batch event.NormalizedBatch, category model.EventCategory) int {
		count := 0
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				if be.EventCategory == category {
					count++
				}
			}
		}
		return count
	}

	testCases := []testCase{
		{
			name:                   "solana-devnet",
			chain:                  model.ChainSolana,
			network:                model.NetworkDevnet,
			address:                "sol-coverage-owner",
			signature:              "sol-coverage-1201",
			sequence:               1201,
			expectedCursor:         "sol-coverage-1201",
			expectedTransferEvents: 3,
			buildSparseEquivalent: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "5000",
					FeePayer:    "sol-coverage-owner",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_1",
							ProgramId:             "11111111111111111111111111111111",
							Address:               "sol-coverage-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-10",
							CounterpartyAddress:   "sol-coverage-counterparty-1",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"decoder_version": "solana-decoder-v0",
							},
						},
						{
							OuterInstructionIndex: 1,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_2",
							ProgramId:             "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
							Address:               "sol-coverage-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "4",
							CounterpartyAddress:   "sol-coverage-counterparty-2",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"decoder_version": "solana-decoder-v0",
							},
						},
						{
							OuterInstructionIndex: 2,
							InnerInstructionIndex: 0,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_3",
							ProgramId:             "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
							Address:               "sol-coverage-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-2",
							CounterpartyAddress:   "sol-coverage-counterparty-3",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"decoder_version": "solana-decoder-v0",
							},
						},
					},
				}
			},
			buildEnrichedComplete: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "5000",
					FeePayer:    "sol-coverage-owner",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 7,
							InnerInstructionIndex: 9,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_1",
							ProgramId:             "11111111111111111111111111111111",
							Address:               "sol-coverage-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-10",
							CounterpartyAddress:   "sol-coverage-counterparty-1",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"event_path":      "outer:0|inner:-1",
								"decoder_version": "solana-decoder-v2",
								"schema_version":  "v2",
							},
						},
						{
							OuterInstructionIndex: 11,
							InnerInstructionIndex: 6,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_2",
							ProgramId:             "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
							Address:               "sol-coverage-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "4",
							CounterpartyAddress:   "sol-coverage-counterparty-2",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"event_path":      "outer:1|inner:-1",
								"decoder_version": "solana-decoder-v2",
								"schema_version":  "v2",
							},
						},
						{
							OuterInstructionIndex: 19,
							InnerInstructionIndex: 4,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_3",
							ProgramId:             "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
							Address:               "sol-coverage-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-2",
							CounterpartyAddress:   "sol-coverage-counterparty-3",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"event_path":      "outer:2|inner:0",
								"decoder_version": "solana-decoder-v2",
								"schema_version":  "v2",
							},
						},
					},
				}
			},
			buildSparsePartial: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "5000",
					FeePayer:    "sol-coverage-owner",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_1",
							ProgramId:             "11111111111111111111111111111111",
							Address:               "sol-coverage-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-10",
							CounterpartyAddress:   "sol-coverage-counterparty-1",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"decoder_version": "solana-decoder-v0",
							},
						},
						{
							OuterInstructionIndex: 1,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_2",
							ProgramId:             "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
							Address:               "sol-coverage-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "4",
							CounterpartyAddress:   "sol-coverage-counterparty-2",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"decoder_version": "solana-decoder-v0",
							},
						},
					},
				}
			},
			buildEnrichedPartial: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "5000",
					FeePayer:    "sol-coverage-owner",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 7,
							InnerInstructionIndex: 9,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_1",
							ProgramId:             "11111111111111111111111111111111",
							Address:               "sol-coverage-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-10",
							CounterpartyAddress:   "sol-coverage-counterparty-1",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"event_path":      "outer:0|inner:-1",
								"decoder_version": "solana-decoder-v2",
								"schema_version":  "v2",
							},
						},
						{
							OuterInstructionIndex: 19,
							InnerInstructionIndex: 4,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_3",
							ProgramId:             "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
							Address:               "sol-coverage-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-2",
							CounterpartyAddress:   "sol-coverage-counterparty-3",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"event_path":      "outer:2|inner:0",
								"decoder_version": "solana-decoder-v2",
								"schema_version":  "v2",
							},
						},
					},
				}
			},
		},
		{
			name:                   "base-sepolia",
			chain:                  model.ChainBase,
			network:                model.NetworkSepolia,
			address:                "0xABCD0000000000000000000000000000000000CC",
			signature:              "ABCD1201",
			sequence:               2201,
			expectedCursor:         "0xabcd1201",
			expectedTransferEvents: 3,
			buildSparseEquivalent: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "100",
					FeePayer:    "0xABCD0000000000000000000000000000000000CC",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_1",
							ProgramId:             "0xABCDEF00000000000000000000000000000000CC",
							Address:               "0xABCD0000000000000000000000000000000000CC",
							ContractAddress:       "0xABCDEF00000000000000000000000000000000CC",
							Delta:                 "-7",
							CounterpartyAddress:   "0xDCBA0000000000000000000000000000000000CC",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"log_index":        "10",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v0",
							},
						},
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_2",
							ProgramId:             "0xABCDEF00000000000000000000000000000000CC",
							Address:               "0xABCD0000000000000000000000000000000000CC",
							ContractAddress:       "0xABCDEF00000000000000000000000000000000CC",
							Delta:                 "3",
							CounterpartyAddress:   "0xDCBA0000000000000000000000000000000000CD",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"log_index":        "11",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v0",
							},
						},
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_3",
							ProgramId:             "0xABCDEF00000000000000000000000000000000CC",
							Address:               "0xABCD0000000000000000000000000000000000CC",
							ContractAddress:       "0xABCDEF00000000000000000000000000000000CC",
							Delta:                 "-1",
							CounterpartyAddress:   "0xDCBA0000000000000000000000000000000000CE",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"log_index":        "12",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v0",
							},
						},
					},
				}
			},
			buildEnrichedComplete: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      "0x" + strings.ToLower(signature),
					BlockCursor: sequence,
					FeeAmount:   "100",
					FeePayer:    "0xabcd0000000000000000000000000000000000cc",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 13,
							InnerInstructionIndex: 0,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_1",
							ProgramId:             "0xabcdef00000000000000000000000000000000cc",
							Address:               "0xabcd0000000000000000000000000000000000cc",
							ContractAddress:       "0xabcdef00000000000000000000000000000000cc",
							Delta:                 "-7",
							CounterpartyAddress:   "0xdcba0000000000000000000000000000000000cc",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"event_path":       "log:10",
								"base_event_path":  "log:10",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v2",
							},
						},
						{
							OuterInstructionIndex: 14,
							InnerInstructionIndex: 0,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_2",
							ProgramId:             "0xabcdef00000000000000000000000000000000cc",
							Address:               "0xabcd0000000000000000000000000000000000cc",
							ContractAddress:       "0xabcdef00000000000000000000000000000000cc",
							Delta:                 "3",
							CounterpartyAddress:   "0xdcba0000000000000000000000000000000000cd",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"event_path":       "log:11",
								"base_event_path":  "log:11",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v2",
							},
						},
						{
							OuterInstructionIndex: 15,
							InnerInstructionIndex: 0,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_3",
							ProgramId:             "0xabcdef00000000000000000000000000000000cc",
							Address:               "0xabcd0000000000000000000000000000000000cc",
							ContractAddress:       "0xabcdef00000000000000000000000000000000cc",
							Delta:                 "-1",
							CounterpartyAddress:   "0xdcba0000000000000000000000000000000000ce",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"event_path":       "log:12",
								"base_event_path":  "log:12",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v2",
							},
						},
					},
				}
			},
			buildSparsePartial: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "100",
					FeePayer:    "0xABCD0000000000000000000000000000000000CC",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_1",
							ProgramId:             "0xABCDEF00000000000000000000000000000000CC",
							Address:               "0xABCD0000000000000000000000000000000000CC",
							ContractAddress:       "0xABCDEF00000000000000000000000000000000CC",
							Delta:                 "-7",
							CounterpartyAddress:   "0xDCBA0000000000000000000000000000000000CC",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"log_index":        "10",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v0",
							},
						},
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_2",
							ProgramId:             "0xABCDEF00000000000000000000000000000000CC",
							Address:               "0xABCD0000000000000000000000000000000000CC",
							ContractAddress:       "0xABCDEF00000000000000000000000000000000CC",
							Delta:                 "3",
							CounterpartyAddress:   "0xDCBA0000000000000000000000000000000000CD",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"log_index":        "11",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v0",
							},
						},
					},
				}
			},
			buildEnrichedPartial: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      "0x" + strings.ToLower(signature),
					BlockCursor: sequence,
					FeeAmount:   "100",
					FeePayer:    "0xabcd0000000000000000000000000000000000cc",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 13,
							InnerInstructionIndex: 0,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_1",
							ProgramId:             "0xabcdef00000000000000000000000000000000cc",
							Address:               "0xabcd0000000000000000000000000000000000cc",
							ContractAddress:       "0xabcdef00000000000000000000000000000000cc",
							Delta:                 "-7",
							CounterpartyAddress:   "0xdcba0000000000000000000000000000000000cc",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"event_path":       "log:10",
								"base_event_path":  "log:10",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v2",
							},
						},
						{
							OuterInstructionIndex: 15,
							InnerInstructionIndex: 0,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_3",
							ProgramId:             "0xabcdef00000000000000000000000000000000cc",
							Address:               "0xabcd0000000000000000000000000000000000cc",
							ContractAddress:       "0xabcdef00000000000000000000000000000000cc",
							Delta:                 "-1",
							CounterpartyAddress:   "0xdcba0000000000000000000000000000000000ce",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"event_path":       "log:12",
								"base_event_path":  "log:12",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v2",
							},
						},
					},
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runOnce := func(resp *sidecarv1.DecodeSolanaTransactionBatchResponse, runBatch event.RawBatch) event.NormalizedBatch {
				ctrl := gomock.NewController(t)
				mockClient := mocks.NewMockChainDecoderClient(ctrl)
				normalizedCh := make(chan event.NormalizedBatch, 1)
				n := &Normalizer{
					sidecarTimeout: 30 * time.Second,
					normalizedCh:   normalizedCh,
					logger:         slog.Default(),
				}
				mockClient.EXPECT().
					DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(resp, nil).
					Times(1)
				require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, runBatch))
				return <-normalizedCh
			}

			batch := event.RawBatch{
				Chain:                  tc.chain,
				Network:                tc.network,
				Address:                tc.address,
				PreviousCursorValue:    strPtr("prior"),
				PreviousCursorSequence: tc.sequence - 1,
				NewCursorValue:         strPtr(tc.signature),
				NewCursorSequence:      tc.sequence,
				RawTransactions: []json.RawMessage{
					json.RawMessage(`{"tx":"coverage"}`),
				},
				Signatures: []event.SignatureInfo{
					{Hash: tc.signature, Sequence: tc.sequence},
				},
			}

			enrichedBaseline := runOnce(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					tc.buildEnrichedComplete(tc.signature, tc.sequence),
				},
			}, batch)

			sparseOnlyEquivalent := runOnce(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					tc.buildSparseEquivalent(tc.signature, tc.sequence),
				},
			}, batch)

			mixedEquivalentSparseFirst := runOnce(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					tc.buildSparseEquivalent(tc.signature, tc.sequence),
					tc.buildEnrichedComplete(tc.signature, tc.sequence),
				},
			}, batch)
			mixedEquivalentEnrichedFirst := runOnce(&sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					tc.buildEnrichedComplete(tc.signature, tc.sequence),
					tc.buildSparseEquivalent(tc.signature, tc.sequence),
				},
			}, batch)

			assert.Equal(t, orderedCanonicalTuples(enrichedBaseline), orderedCanonicalTuples(sparseOnlyEquivalent))
			assert.Equal(t, orderedCanonicalTuples(enrichedBaseline), orderedCanonicalTuples(mixedEquivalentSparseFirst))
			assert.Equal(t, orderedCanonicalTuples(mixedEquivalentSparseFirst), orderedCanonicalTuples(mixedEquivalentEnrichedFirst))

			assertNoDuplicateCanonicalIDs(t, enrichedBaseline)
			assertNoDuplicateCanonicalIDs(t, sparseOnlyEquivalent)
			assertNoDuplicateCanonicalIDs(t, mixedEquivalentSparseFirst)
			assertNoDuplicateCanonicalIDs(t, mixedEquivalentEnrichedFirst)

			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)
			normalizedCh := make(chan event.NormalizedBatch, 3)
			n := &Normalizer{
				sidecarTimeout: 30 * time.Second,
				normalizedCh:   normalizedCh,
				logger:         slog.Default(),
			}

			responses := []*sidecarv1.DecodeSolanaTransactionBatchResponse{
				{
					Results: []*sidecarv1.TransactionResult{
						tc.buildSparsePartial(tc.signature, tc.sequence),
					},
				},
				{
					Results: []*sidecarv1.TransactionResult{
						tc.buildSparsePartial(tc.signature, tc.sequence),
						tc.buildEnrichedPartial(tc.signature, tc.sequence),
					},
				},
				{
					Results: []*sidecarv1.TransactionResult{
						tc.buildEnrichedPartial(tc.signature, tc.sequence),
						tc.buildSparsePartial(tc.signature, tc.sequence),
					},
				},
			}
			call := 0
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(len(responses)).
				DoAndReturn(func(context.Context, *sidecarv1.DecodeSolanaTransactionBatchRequest, ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					resp := responses[call]
					call++
					return resp, nil
				})

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			sparseRun := <-normalizedCh

			firstReplayBatch := batch
			firstReplayBatch.PreviousCursorValue = sparseRun.NewCursorValue
			firstReplayBatch.PreviousCursorSequence = sparseRun.NewCursorSequence
			firstReplayBatch.NewCursorValue = sparseRun.NewCursorValue
			firstReplayBatch.NewCursorSequence = sparseRun.NewCursorSequence

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, firstReplayBatch))
			convergedRun := <-normalizedCh

			secondReplayBatch := batch
			secondReplayBatch.PreviousCursorValue = convergedRun.NewCursorValue
			secondReplayBatch.PreviousCursorSequence = convergedRun.NewCursorSequence
			secondReplayBatch.NewCursorValue = convergedRun.NewCursorValue
			secondReplayBatch.NewCursorSequence = convergedRun.NewCursorSequence

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, secondReplayBatch))
			replayConvergedRun := <-normalizedCh

			assert.NotEqual(t, canonicalEventIDSet(enrichedBaseline), canonicalEventIDSet(sparseRun))
			assert.Equal(t, canonicalEventIDSet(enrichedBaseline), canonicalEventIDSet(convergedRun))
			assert.Equal(t, canonicalEventIDSet(convergedRun), canonicalEventIDSet(replayConvergedRun))

			assert.Greater(
				t,
				len(canonicalEventIDSet(convergedRun)),
				len(canonicalEventIDSet(sparseRun)),
				"incremental enriched coverage should emit newly discovered events once while preserving existing canonical IDs",
			)

			assertNoDuplicateCanonicalIDs(t, sparseRun)
			assertNoDuplicateCanonicalIDs(t, convergedRun)
			assertNoDuplicateCanonicalIDs(t, replayConvergedRun)

			assert.Equal(t, tc.expectedTransferEvents, countCategory(convergedRun, model.EventCategoryTransfer))
			assert.Equal(t, tc.expectedTransferEvents, countCategory(replayConvergedRun, model.EventCategoryTransfer))

			if tc.chain == model.ChainSolana {
				assert.Equal(t, 1, countCategory(convergedRun, model.EventCategoryFee))
				assert.Equal(t, 1, countCategory(replayConvergedRun, model.EventCategoryFee))
			} else {
				assert.Equal(t, 1, countCategory(convergedRun, model.EventCategoryFeeExecutionL2))
				assert.Equal(t, 1, countCategory(convergedRun, model.EventCategoryFeeDataL1))
				assert.Equal(t, 1, countCategory(replayConvergedRun, model.EventCategoryFeeExecutionL2))
				assert.Equal(t, 1, countCategory(replayConvergedRun, model.EventCategoryFeeDataL1))
			}

			require.NotNil(t, sparseRun.NewCursorValue)
			require.NotNil(t, convergedRun.NewCursorValue)
			require.NotNil(t, replayConvergedRun.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *sparseRun.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *convergedRun.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *replayConvergedRun.NewCursorValue)
			assert.GreaterOrEqual(t, sparseRun.NewCursorSequence, sparseRun.PreviousCursorSequence)
			assert.GreaterOrEqual(t, convergedRun.NewCursorSequence, convergedRun.PreviousCursorSequence)
			assert.GreaterOrEqual(t, replayConvergedRun.NewCursorSequence, replayConvergedRun.PreviousCursorSequence)
			assert.GreaterOrEqual(t, replayConvergedRun.NewCursorSequence, convergedRun.NewCursorSequence)
		})
	}
}

func TestProcessBatch_TriChainDelayedEnrichmentIsolationConvergesWithEnrichedFirstBaseline(t *testing.T) {
	const (
		baseAddress          = "0xABCD000000000000000000000000000000003301"
		baseSignature        = "ABCD3301"
		baseSequence   int64 = 3301
		baseCursorPrev       = "ABCD3300"

		solanaAddress         = "sol-delay-owner"
		solanaSignature       = "sol-delay-3301"
		solanaSequence  int64 = 4301

		btcAddress         = "tb1-delay-owner"
		btcSignature       = "0xABCD5501"
		btcSequence  int64 = 5501
	)

	type scenarioOutput struct {
		baseInitial   event.NormalizedBatch
		baseConverged event.NormalizedBatch
		solana        event.NormalizedBatch
		btc           event.NormalizedBatch
	}

	canonicalEventIDSet := func(batch event.NormalizedBatch) map[string]struct{} {
		out := make(map[string]struct{})
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				out[be.EventID] = struct{}{}
			}
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
	countCategory := func(batch event.NormalizedBatch, category model.EventCategory) int {
		count := 0
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				if be.EventCategory == category {
					count++
				}
			}
		}
		return count
	}

	basePartial := func() *sidecarv1.TransactionResult {
		return &sidecarv1.TransactionResult{
			TxHash:      baseSignature,
			BlockCursor: baseSequence,
			FeeAmount:   "100",
			FeePayer:    baseAddress,
			Status:      "SUCCESS",
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					OuterInstructionIndex: 0,
					InnerInstructionIndex: -1,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "base_partial_transfer_1",
					ProgramId:             "0xabcdef0000000000000000000000000000003301",
					Address:               baseAddress,
					ContractAddress:       "0xabcdef0000000000000000000000000000003301",
					Delta:                 "-7",
					CounterpartyAddress:   "0xdcba000000000000000000000000000000003301",
					TokenDecimals:         18,
					TokenType:             string(model.TokenTypeFungible),
					Metadata: map[string]string{
						"fee_execution_l2": "70",
						"fee_data_l1":      "30",
						"decoder_version":  "base-decoder-v0",
					},
				},
			},
		}
	}
	baseEnriched := func() *sidecarv1.TransactionResult {
		return &sidecarv1.TransactionResult{
			TxHash:      "0x" + strings.ToLower(baseSignature),
			BlockCursor: baseSequence,
			FeeAmount:   "100",
			FeePayer:    strings.ToLower(baseAddress),
			Status:      "SUCCESS",
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					OuterInstructionIndex: 13,
					InnerInstructionIndex: 0,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "base_enriched_transfer_1",
					ProgramId:             "0xabcdef0000000000000000000000000000003301",
					Address:               strings.ToLower(baseAddress),
					ContractAddress:       "0xabcdef0000000000000000000000000000003301",
					Delta:                 "-7",
					CounterpartyAddress:   "0xdcba000000000000000000000000000000003301",
					TokenDecimals:         18,
					TokenType:             string(model.TokenTypeFungible),
					Metadata: map[string]string{
						"event_path":       "log:10",
						"base_event_path":  "log:10",
						"fee_execution_l2": "70",
						"fee_data_l1":      "30",
						"decoder_version":  "base-decoder-v2",
					},
				},
				{
					OuterInstructionIndex: 14,
					InnerInstructionIndex: 0,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "base_enriched_transfer_2",
					ProgramId:             "0xabcdef0000000000000000000000000000003301",
					Address:               strings.ToLower(baseAddress),
					ContractAddress:       "0xabcdef0000000000000000000000000000003301",
					Delta:                 "3",
					CounterpartyAddress:   "0xdcba000000000000000000000000000000003302",
					TokenDecimals:         18,
					TokenType:             string(model.TokenTypeFungible),
					Metadata: map[string]string{
						"event_path":       "log:11",
						"base_event_path":  "log:11",
						"fee_execution_l2": "70",
						"fee_data_l1":      "30",
						"decoder_version":  "base-decoder-v2",
					},
				},
			},
		}
	}
	solanaEnriched := func() *sidecarv1.TransactionResult {
		return &sidecarv1.TransactionResult{
			TxHash:      solanaSignature,
			BlockCursor: solanaSequence,
			FeeAmount:   "5000",
			FeePayer:    solanaAddress,
			Status:      "SUCCESS",
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					OuterInstructionIndex: 7,
					InnerInstructionIndex: 9,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "solana_enriched_transfer",
					ProgramId:             "11111111111111111111111111111111",
					Address:               solanaAddress,
					ContractAddress:       "So11111111111111111111111111111111111111112",
					Delta:                 "-9",
					CounterpartyAddress:   "sol-delay-counterparty",
					TokenSymbol:           "SOL",
					TokenName:             "Solana",
					TokenDecimals:         9,
					TokenType:             string(model.TokenTypeNative),
					Metadata: map[string]string{
						"event_path":      "outer:0|inner:-1",
						"decoder_version": "solana-decoder-v2",
					},
				},
			},
		}
	}
	btcEnriched := func() *sidecarv1.TransactionResult {
		return &sidecarv1.TransactionResult{
			TxHash:      "ABCD5501",
			BlockCursor: btcSequence,
			FeeAmount:   "100",
			FeePayer:    btcAddress,
			Status:      "SUCCESS",
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					OuterInstructionIndex: 0,
					InnerInstructionIndex: -1,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "vin_spend",
					ProgramId:             "btc",
					Address:               btcAddress,
					ContractAddress:       "BTC",
					Delta:                 "-1000",
					TokenSymbol:           "BTC",
					TokenName:             "Bitcoin",
					TokenDecimals:         8,
					TokenType:             string(model.TokenTypeNative),
					Metadata: map[string]string{
						"event_path":     "vin:0",
						"finality_state": "confirmed",
					},
				},
				{
					OuterInstructionIndex: 1,
					InnerInstructionIndex: -1,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "vout_receive",
					ProgramId:             "btc",
					Address:               btcAddress,
					ContractAddress:       "BTC",
					Delta:                 "900",
					TokenSymbol:           "BTC",
					TokenName:             "Bitcoin",
					TokenDecimals:         8,
					TokenType:             string(model.TokenTypeNative),
					Metadata: map[string]string{
						"event_path":     "vout:1",
						"finality_state": "confirmed",
					},
				},
			},
		}
	}

	baseBatch := event.RawBatch{
		Chain:                  model.ChainBase,
		Network:                model.NetworkSepolia,
		Address:                baseAddress,
		PreviousCursorValue:    strPtr(baseCursorPrev),
		PreviousCursorSequence: baseSequence - 1,
		NewCursorValue:         strPtr(baseSignature),
		NewCursorSequence:      baseSequence,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"base-delayed-enrichment"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: baseSignature, Sequence: baseSequence},
		},
	}
	solanaBatch := event.RawBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		Address:                solanaAddress,
		PreviousCursorValue:    strPtr("sol-delay-3300"),
		PreviousCursorSequence: solanaSequence - 1,
		NewCursorValue:         strPtr(solanaSignature),
		NewCursorSequence:      solanaSequence,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"solana-delayed-enrichment"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: solanaSignature, Sequence: solanaSequence},
		},
	}
	btcBatch := event.RawBatch{
		Chain:                  model.ChainBTC,
		Network:                model.NetworkTestnet,
		Address:                btcAddress,
		PreviousCursorValue:    strPtr("ABCD5500"),
		PreviousCursorSequence: btcSequence - 1,
		NewCursorValue:         strPtr(btcSignature),
		NewCursorSequence:      btcSequence,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"btc-delayed-enrichment"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: btcSignature, Sequence: btcSequence},
		},
	}

	runScenario := func(t *testing.T, partialFirst bool) scenarioOutput {
		t.Helper()

		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)
		normalizedCh := make(chan event.NormalizedBatch, 4)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		firstBaseResult := baseEnriched()
		if partialFirst {
			firstBaseResult = basePartial()
		}

		responses := []*sidecarv1.DecodeSolanaTransactionBatchResponse{
			{Results: []*sidecarv1.TransactionResult{firstBaseResult}},
			{Results: []*sidecarv1.TransactionResult{solanaEnriched()}},
			{Results: []*sidecarv1.TransactionResult{btcEnriched()}},
			{Results: []*sidecarv1.TransactionResult{baseEnriched()}},
		}
		expectedSignatures := []string{
			baseSignature,
			solanaSignature,
			btcSignature,
			baseSignature,
		}

		call := 0
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(len(responses)).
			DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
				require.Len(t, req.GetTransactions(), 1)
				assert.Equal(t, expectedSignatures[call], req.GetTransactions()[0].GetSignature())
				resp := responses[call]
				call++
				return resp, nil
			})

		require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, baseBatch))
		baseInitial := <-normalizedCh

		require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, solanaBatch))
		solanaRun := <-normalizedCh

		require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, btcBatch))
		btcRun := <-normalizedCh

		baseReplayBatch := baseBatch
		baseReplayBatch.PreviousCursorValue = baseInitial.NewCursorValue
		baseReplayBatch.PreviousCursorSequence = baseInitial.NewCursorSequence
		baseReplayBatch.NewCursorValue = baseInitial.NewCursorValue
		baseReplayBatch.NewCursorSequence = baseInitial.NewCursorSequence

		require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, baseReplayBatch))
		baseConverged := <-normalizedCh

		assertNoDuplicateCanonicalIDs(t, baseInitial)
		assertNoDuplicateCanonicalIDs(t, baseConverged)
		assertNoDuplicateCanonicalIDs(t, solanaRun)
		assertNoDuplicateCanonicalIDs(t, btcRun)

		return scenarioOutput{
			baseInitial:   baseInitial,
			baseConverged: baseConverged,
			solana:        solanaRun,
			btc:           btcRun,
		}
	}

	partialFirst := runScenario(t, true)
	enrichedFirst := runScenario(t, false)

	assert.Equal(t, orderedCanonicalTuples(enrichedFirst.baseConverged), orderedCanonicalTuples(partialFirst.baseConverged))
	assert.Equal(t, orderedCanonicalTuples(enrichedFirst.solana), orderedCanonicalTuples(partialFirst.solana))
	assert.Equal(t, orderedCanonicalTuples(enrichedFirst.btc), orderedCanonicalTuples(partialFirst.btc))

	assert.Less(
		t,
		len(canonicalEventIDSet(partialFirst.baseInitial)),
		len(canonicalEventIDSet(partialFirst.baseConverged)),
		"delayed base enrichment should add only previously missing logical events",
	)
	assert.Equal(t, 2, countCategory(partialFirst.baseConverged, model.EventCategoryTransfer))
	assert.Equal(t, 1, countCategory(partialFirst.baseConverged, model.EventCategoryFeeExecutionL2))
	assert.Equal(t, 1, countCategory(partialFirst.baseConverged, model.EventCategoryFeeDataL1))
	assert.Equal(t, 1, countCategory(partialFirst.solana, model.EventCategoryFee))
	assert.Equal(t, 2, countCategory(partialFirst.btc, model.EventCategoryTransfer))

	require.NotNil(t, partialFirst.baseConverged.NewCursorValue)
	require.NotNil(t, partialFirst.solana.NewCursorValue)
	require.NotNil(t, partialFirst.btc.NewCursorValue)
	assert.Equal(t, "0xabcd3301", *partialFirst.baseConverged.NewCursorValue)
	assert.Equal(t, solanaSignature, *partialFirst.solana.NewCursorValue)
	assert.Equal(t, "abcd5501", *partialFirst.btc.NewCursorValue)
	assert.GreaterOrEqual(t, partialFirst.baseConverged.NewCursorSequence, partialFirst.baseInitial.NewCursorSequence)
}

func TestProcessBatch_DecodeCoverageRegressionFlapPreservesEnrichedFloorAcrossMandatoryChains(t *testing.T) {
	type testCase struct {
		name           string
		chain          model.Chain
		network        model.Network
		address        string
		signature      string
		sequence       int64
		expectedCursor string
		buildEnriched  func(string, int64) *sidecarv1.TransactionResult
		buildSparse    func(string, int64) *sidecarv1.TransactionResult
	}

	canonicalEventIDSet := func(batch event.NormalizedBatch) map[string]struct{} {
		out := make(map[string]struct{})
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				out[be.EventID] = struct{}{}
			}
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
	countCategory := func(batch event.NormalizedBatch, category model.EventCategory) int {
		count := 0
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				if be.EventCategory == category {
					count++
				}
			}
		}
		return count
	}

	testCases := []testCase{
		{
			name:           "solana-devnet",
			chain:          model.ChainSolana,
			network:        model.NetworkDevnet,
			address:        "sol-flap-owner",
			signature:      "sol-flap-9001",
			sequence:       9001,
			expectedCursor: "sol-flap-9001",
			buildEnriched: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "5000",
					FeePayer:    "sol-flap-owner",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 7,
							InnerInstructionIndex: 4,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_1",
							ProgramId:             "11111111111111111111111111111111",
							Address:               "sol-flap-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-8",
							CounterpartyAddress:   "sol-flap-counterparty-1",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"event_path":      "outer:0|inner:-1",
								"decoder_version": "solana-decoder-v2",
							},
						},
						{
							OuterInstructionIndex: 9,
							InnerInstructionIndex: 3,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_2",
							ProgramId:             "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
							Address:               "sol-flap-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "3",
							CounterpartyAddress:   "sol-flap-counterparty-2",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"event_path":      "outer:1|inner:-1",
								"decoder_version": "solana-decoder-v2",
							},
						},
					},
				}
			},
			buildSparse: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "5000",
					FeePayer:    "sol-flap-owner",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_1",
							ProgramId:             "11111111111111111111111111111111",
							Address:               "sol-flap-owner",
							ContractAddress:       "So11111111111111111111111111111111111111112",
							Delta:                 "-8",
							CounterpartyAddress:   "sol-flap-counterparty-1",
							TokenSymbol:           "SOL",
							TokenName:             "Solana",
							TokenDecimals:         9,
							TokenType:             string(model.TokenTypeNative),
							Metadata: map[string]string{
								"decoder_version": "solana-decoder-v0",
							},
						},
					},
				}
			},
		},
		{
			name:           "base-sepolia",
			chain:          model.ChainBase,
			network:        model.NetworkSepolia,
			address:        "0xABCD000000000000000000000000000000009001",
			signature:      "ABCD9001",
			sequence:       9101,
			expectedCursor: "0xabcd9001",
			buildEnriched: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      "0x" + strings.ToLower(signature),
					BlockCursor: sequence,
					FeeAmount:   "100",
					FeePayer:    "0xabcd000000000000000000000000000000009001",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 13,
							InnerInstructionIndex: 0,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_1",
							ProgramId:             "0xabcdef0000000000000000000000000000009001",
							Address:               "0xabcd000000000000000000000000000000009001",
							ContractAddress:       "0xabcdef0000000000000000000000000000009001",
							Delta:                 "-6",
							CounterpartyAddress:   "0xdcba000000000000000000000000000000009001",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"event_path":       "log:10",
								"base_event_path":  "log:10",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v2",
							},
						},
						{
							OuterInstructionIndex: 14,
							InnerInstructionIndex: 0,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "enriched_transfer_2",
							ProgramId:             "0xabcdef0000000000000000000000000000009001",
							Address:               "0xabcd000000000000000000000000000000009001",
							ContractAddress:       "0xabcdef0000000000000000000000000000009001",
							Delta:                 "2",
							CounterpartyAddress:   "0xdcba000000000000000000000000000000009002",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"event_path":       "log:11",
								"base_event_path":  "log:11",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v2",
							},
						},
					},
				}
			},
			buildSparse: func(signature string, sequence int64) *sidecarv1.TransactionResult {
				return &sidecarv1.TransactionResult{
					TxHash:      signature,
					BlockCursor: sequence,
					FeeAmount:   "100",
					FeePayer:    "0xABCD000000000000000000000000000000009001",
					Status:      "SUCCESS",
					BalanceEvents: []*sidecarv1.BalanceEventInfo{
						{
							OuterInstructionIndex: 0,
							InnerInstructionIndex: -1,
							EventCategory:         string(model.EventCategoryTransfer),
							EventAction:           "sparse_transfer_1",
							ProgramId:             "0xABCDEF0000000000000000000000000000009001",
							Address:               "0xABCD000000000000000000000000000000009001",
							ContractAddress:       "0xABCDEF0000000000000000000000000000009001",
							Delta:                 "-6",
							CounterpartyAddress:   "0xDCBA000000000000000000000000000000009001",
							TokenDecimals:         18,
							TokenType:             string(model.TokenTypeFungible),
							Metadata: map[string]string{
								"log_index":        "10",
								"fee_execution_l2": "70",
								"fee_data_l1":      "30",
								"decoder_version":  "base-decoder-v0",
							},
						},
					},
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)
			normalizedCh := make(chan event.NormalizedBatch, 3)
			n := &Normalizer{
				sidecarTimeout: 30 * time.Second,
				normalizedCh:   normalizedCh,
				logger:         slog.Default(),
			}

			responses := []*sidecarv1.DecodeSolanaTransactionBatchResponse{
				{
					Results: []*sidecarv1.TransactionResult{
						tc.buildEnriched(tc.signature, tc.sequence),
					},
				},
				{
					Results: []*sidecarv1.TransactionResult{
						tc.buildSparse(tc.signature, tc.sequence),
					},
				},
				{
					Results: []*sidecarv1.TransactionResult{
						tc.buildEnriched(tc.signature, tc.sequence),
					},
				},
			}
			call := 0
			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(len(responses)).
				DoAndReturn(func(context.Context, *sidecarv1.DecodeSolanaTransactionBatchRequest, ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					resp := responses[call]
					call++
					return resp, nil
				})

			batch := event.RawBatch{
				Chain:                  tc.chain,
				Network:                tc.network,
				Address:                tc.address,
				PreviousCursorValue:    strPtr("prior"),
				PreviousCursorSequence: tc.sequence - 1,
				NewCursorValue:         strPtr(tc.signature),
				NewCursorSequence:      tc.sequence,
				RawTransactions: []json.RawMessage{
					json.RawMessage(`{"tx":"coverage-flap"}`),
				},
				Signatures: []event.SignatureInfo{
					{Hash: tc.signature, Sequence: tc.sequence},
				},
			}

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			enrichedRun := <-normalizedCh

			sparseReplay := batch
			sparseReplay.PreviousCursorValue = enrichedRun.NewCursorValue
			sparseReplay.PreviousCursorSequence = enrichedRun.NewCursorSequence
			sparseReplay.NewCursorValue = enrichedRun.NewCursorValue
			sparseReplay.NewCursorSequence = enrichedRun.NewCursorSequence
			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, sparseReplay))
			sparseRun := <-normalizedCh

			reEnrichedReplay := batch
			reEnrichedReplay.PreviousCursorValue = sparseRun.NewCursorValue
			reEnrichedReplay.PreviousCursorSequence = sparseRun.NewCursorSequence
			reEnrichedReplay.NewCursorValue = sparseRun.NewCursorValue
			reEnrichedReplay.NewCursorSequence = sparseRun.NewCursorSequence
			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, reEnrichedReplay))
			reEnrichedRun := <-normalizedCh

			assert.Equal(t, canonicalEventIDSet(enrichedRun), canonicalEventIDSet(sparseRun))
			assert.Equal(t, canonicalEventIDSet(enrichedRun), canonicalEventIDSet(reEnrichedRun))

			assertNoDuplicateCanonicalIDs(t, enrichedRun)
			assertNoDuplicateCanonicalIDs(t, sparseRun)
			assertNoDuplicateCanonicalIDs(t, reEnrichedRun)

			assert.Equal(t, 2, countCategory(sparseRun, model.EventCategoryTransfer))
			assert.Equal(t, 2, countCategory(reEnrichedRun, model.EventCategoryTransfer))
			if tc.chain == model.ChainSolana {
				assert.Equal(t, 1, countCategory(sparseRun, model.EventCategoryFee))
				assert.Equal(t, 1, countCategory(reEnrichedRun, model.EventCategoryFee))
			} else {
				assert.Equal(t, 1, countCategory(sparseRun, model.EventCategoryFeeExecutionL2))
				assert.Equal(t, 1, countCategory(sparseRun, model.EventCategoryFeeDataL1))
				assert.Equal(t, 1, countCategory(reEnrichedRun, model.EventCategoryFeeExecutionL2))
				assert.Equal(t, 1, countCategory(reEnrichedRun, model.EventCategoryFeeDataL1))
			}

			require.NotNil(t, sparseRun.NewCursorValue)
			require.NotNil(t, reEnrichedRun.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *sparseRun.NewCursorValue)
			assert.Equal(t, tc.expectedCursor, *reEnrichedRun.NewCursorValue)
			assert.GreaterOrEqual(t, sparseRun.NewCursorSequence, sparseRun.PreviousCursorSequence)
			assert.GreaterOrEqual(t, reEnrichedRun.NewCursorSequence, reEnrichedRun.PreviousCursorSequence)
			assert.GreaterOrEqual(t, reEnrichedRun.NewCursorSequence, sparseRun.NewCursorSequence)
		})
	}
}

func TestCanonicalSignatureIdentity_BTCTxIDIsLowercased(t *testing.T) {
	assert.Equal(t, "abcdef0011", canonicalSignatureIdentity(model.ChainBTC, "ABCDEF0011"))
	assert.Equal(t, "abcdef0011", canonicalSignatureIdentity(model.ChainBTC, "0xABCDEF0011"))
}

func TestProcessBatch_BTCReplayResumeDeterministicTupleEquivalenceAndNoDuplicateCanonicalIDs(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := mocks.NewMockChainDecoderClient(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 3)
	n := &Normalizer{
		sidecarTimeout: 30 * time.Second,
		normalizedCh:   normalizedCh,
		logger:         slog.Default(),
	}

	response := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			{
				TxHash:      "ABCD9001",
				BlockCursor: 9001,
				FeeAmount:   "100",
				FeePayer:    "tb1-replay-owner",
				Status:      "SUCCESS",
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						EventCategory:         string(model.EventCategoryTransfer),
						EventAction:           "vin_spend",
						ProgramId:             "btc",
						Address:               "tb1-replay-owner",
						ContractAddress:       "BTC",
						Delta:                 "-1100",
						TokenSymbol:           "BTC",
						TokenName:             "Bitcoin",
						TokenDecimals:         8,
						TokenType:             string(model.TokenTypeNative),
						Metadata: map[string]string{
							"event_path":     "vin:0",
							"finality_state": "confirmed",
						},
					},
					{
						OuterInstructionIndex: 1,
						InnerInstructionIndex: -1,
						EventCategory:         string(model.EventCategoryTransfer),
						EventAction:           "vout_receive",
						ProgramId:             "btc",
						Address:               "tb1-replay-owner",
						ContractAddress:       "BTC",
						Delta:                 "1000",
						TokenSymbol:           "BTC",
						TokenName:             "Bitcoin",
						TokenDecimals:         8,
						TokenType:             string(model.TokenTypeNative),
						Metadata: map[string]string{
							"event_path":     "vout:1",
							"finality_state": "confirmed",
						},
					},
				},
			},
		},
	}

	mockClient.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		Times(3).
		DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
			require.Len(t, req.GetTransactions(), 1)
			assert.Equal(t, "abcd9001", canonicalSignatureIdentity(model.ChainBTC, req.GetTransactions()[0].GetSignature()))
			return response, nil
		})

	baseBatch := event.RawBatch{
		Chain:                  model.ChainBTC,
		Network:                model.NetworkTestnet,
		Address:                "tb1-replay-owner",
		PreviousCursorValue:    strPtr("0xABCD9000"),
		PreviousCursorSequence: 9000,
		NewCursorValue:         strPtr("0xABCD9001"),
		NewCursorSequence:      9001,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{"tx":"btc-replay-proof"}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "0xABCD9001", Sequence: 9001},
		},
	}

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, baseBatch))
	firstRun := <-normalizedCh

	repeatedRunBatch := baseBatch
	repeatedRunBatch.Signatures = []event.SignatureInfo{
		{Hash: "ABCD9001", Sequence: 9001},
	}
	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, repeatedRunBatch))
	secondRun := <-normalizedCh

	replayBoundaryBatch := baseBatch
	replayBoundaryBatch.PreviousCursorValue = firstRun.NewCursorValue
	replayBoundaryBatch.PreviousCursorSequence = firstRun.NewCursorSequence
	replayBoundaryBatch.NewCursorValue = firstRun.NewCursorValue
	replayBoundaryBatch.NewCursorSequence = firstRun.NewCursorSequence
	replayBoundaryBatch.Signatures = []event.SignatureInfo{
		{Hash: "ABCD9001", Sequence: 9001},
	}
	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, replayBoundaryBatch))
	thirdRun := <-normalizedCh

	assertNoDuplicateCanonicalIDs := func(run event.NormalizedBatch) {
		t.Helper()
		seen := make(map[string]struct{})
		for _, tx := range run.Transactions {
			for _, be := range tx.BalanceEvents {
				_, exists := seen[be.EventID]
				require.False(t, exists, "duplicate canonical event id in run: %s", be.EventID)
				seen[be.EventID] = struct{}{}
			}
		}
	}

	assertNoDuplicateCanonicalIDs(firstRun)
	assertNoDuplicateCanonicalIDs(secondRun)
	assertNoDuplicateCanonicalIDs(thirdRun)
	assert.Equal(t, orderedCanonicalTuples(firstRun), orderedCanonicalTuples(secondRun))
	assert.Equal(t, orderedCanonicalTuples(firstRun), orderedCanonicalTuples(thirdRun))
	assert.GreaterOrEqual(t, secondRun.NewCursorSequence, secondRun.PreviousCursorSequence)
	assert.GreaterOrEqual(t, thirdRun.NewCursorSequence, thirdRun.PreviousCursorSequence)

	for _, run := range []event.NormalizedBatch{firstRun, secondRun, thirdRun} {
		require.NotNil(t, run.NewCursorValue)
		assert.Equal(t, "abcd9001", *run.NewCursorValue)
		require.Len(t, run.Transactions, 1)
		assert.Equal(t, "abcd9001", run.Transactions[0].TxHash)
	}
}

func TestProcessBatch_BTCSignedDeltaConservation_CoinbaseMultiInputOutputChangeAndFeeAttribution(t *testing.T) {
	type testCase struct {
		name               string
		address            string
		signature          string
		sequence           int64
		result             *sidecarv1.TransactionResult
		expectedNetDelta   string
		expectedFee        string
		expectedFeePayer   string
		expectedEventPaths []string
		expectFeeConserves bool
	}

	testCases := []testCase{
		{
			name:      "coinbase_reward_mint",
			address:   "tb1-miner",
			signature: "0xCOINBASE9001",
			sequence:  9101,
			result: &sidecarv1.TransactionResult{
				TxHash:      "COINBASE9001",
				BlockCursor: 9101,
				FeeAmount:   "0",
				FeePayer:    "",
				Status:      "SUCCESS",
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						EventCategory:         string(model.EventCategoryTransfer),
						EventAction:           "vout_receive",
						ProgramId:             "btc",
						Address:               "tb1-miner",
						ContractAddress:       "BTC",
						Delta:                 "625000000",
						TokenSymbol:           "BTC",
						TokenName:             "Bitcoin",
						TokenDecimals:         8,
						TokenType:             string(model.TokenTypeNative),
						Metadata: map[string]string{
							"event_path":     "vout:0",
							"finality_state": "confirmed",
						},
					},
				},
			},
			expectedNetDelta:   "625000000",
			expectedFee:        "0",
			expectedFeePayer:   "",
			expectedEventPaths: []string{"vout:0"},
		},
		{
			name:      "multi_input_output_with_change_and_fee",
			address:   "tb1-spender",
			signature: "FEEC0DE9002",
			sequence:  9102,
			result: &sidecarv1.TransactionResult{
				TxHash:      "0xFEEC0DE9002",
				BlockCursor: 9102,
				FeeAmount:   "50",
				FeePayer:    "tb1-spender",
				Status:      "SUCCESS",
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						EventCategory:         string(model.EventCategoryTransfer),
						EventAction:           "vin_spend",
						ProgramId:             "btc",
						Address:               "tb1-spender",
						ContractAddress:       "BTC",
						Delta:                 "-700",
						TokenSymbol:           "BTC",
						TokenName:             "Bitcoin",
						TokenDecimals:         8,
						TokenType:             string(model.TokenTypeNative),
						Metadata: map[string]string{
							"event_path":     "vin:0",
							"finality_state": "confirmed",
						},
					},
					{
						OuterInstructionIndex: 1,
						InnerInstructionIndex: -1,
						EventCategory:         string(model.EventCategoryTransfer),
						EventAction:           "vin_spend",
						ProgramId:             "btc",
						Address:               "tb1-spender",
						ContractAddress:       "BTC",
						Delta:                 "-400",
						TokenSymbol:           "BTC",
						TokenName:             "Bitcoin",
						TokenDecimals:         8,
						TokenType:             string(model.TokenTypeNative),
						Metadata: map[string]string{
							"event_path":     "vin:1",
							"finality_state": "confirmed",
						},
					},
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						EventCategory:         string(model.EventCategoryTransfer),
						EventAction:           "vout_receive",
						ProgramId:             "btc",
						Address:               "tb1-merchant",
						ContractAddress:       "BTC",
						Delta:                 "1000",
						TokenSymbol:           "BTC",
						TokenName:             "Bitcoin",
						TokenDecimals:         8,
						TokenType:             string(model.TokenTypeNative),
						Metadata: map[string]string{
							"event_path":     "vout:0",
							"finality_state": "confirmed",
						},
					},
					{
						OuterInstructionIndex: 1,
						InnerInstructionIndex: -1,
						EventCategory:         string(model.EventCategoryTransfer),
						EventAction:           "vout_receive",
						ProgramId:             "btc",
						Address:               "tb1-spender",
						ContractAddress:       "BTC",
						Delta:                 "50",
						TokenSymbol:           "BTC",
						TokenName:             "Bitcoin",
						TokenDecimals:         8,
						TokenType:             string(model.TokenTypeNative),
						Metadata: map[string]string{
							"event_path":     "vout:1",
							"finality_state": "confirmed",
						},
					},
				},
			},
			expectedNetDelta:   "-50",
			expectedFee:        "50",
			expectedFeePayer:   "tb1-spender",
			expectedEventPaths: []string{"vin:0", "vin:1", "vout:0", "vout:1"},
			expectFeeConserves: true,
		},
	}

	parseDecimal := func(t *testing.T, value string) *big.Int {
		t.Helper()
		out, ok := new(big.Int).SetString(strings.TrimSpace(value), 10)
		require.True(t, ok, "invalid decimal: %q", value)
		return out
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := mocks.NewMockChainDecoderClient(ctrl)

			normalizedCh := make(chan event.NormalizedBatch, 1)
			n := &Normalizer{
				sidecarTimeout: 30 * time.Second,
				normalizedCh:   normalizedCh,
				logger:         slog.Default(),
			}

			mockClient.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&sidecarv1.DecodeSolanaTransactionBatchResponse{
					Results: []*sidecarv1.TransactionResult{tc.result},
				}, nil).
				Times(1)

			rawBatch := event.RawBatch{
				Chain:                  model.ChainBTC,
				Network:                model.NetworkTestnet,
				Address:                tc.address,
				PreviousCursorValue:    strPtr("btc-delta-prev"),
				PreviousCursorSequence: tc.sequence - 1,
				NewCursorValue:         strPtr(tc.signature),
				NewCursorSequence:      tc.sequence,
				RawTransactions: []json.RawMessage{
					json.RawMessage(`{"tx":"btc-signed-delta-proof"}`),
				},
				Signatures: []event.SignatureInfo{
					{Hash: tc.signature, Sequence: tc.sequence},
				},
			}

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, rawBatch))
			normalized := <-normalizedCh
			require.Len(t, normalized.Transactions, 1)

			tx := normalized.Transactions[0]
			assert.Equal(t, tc.expectedFee, tx.FeeAmount)
			assert.Equal(t, tc.expectedFeePayer, tx.FeePayer)

			seenPaths := make(map[string]struct{}, len(tx.BalanceEvents))
			seenEventIDs := make(map[string]struct{}, len(tx.BalanceEvents))

			netDelta := big.NewInt(0)
			totalVinSpend := big.NewInt(0)
			totalVoutReceive := big.NewInt(0)

			for _, be := range tx.BalanceEvents {
				assert.Equal(t, model.EventCategoryTransfer, be.EventCategory)
				seenPaths[be.EventPath] = struct{}{}
				_, exists := seenEventIDs[be.EventID]
				require.False(t, exists, "duplicate canonical event id found: %s", be.EventID)
				seenEventIDs[be.EventID] = struct{}{}

				delta := parseDecimal(t, be.Delta)
				netDelta.Add(netDelta, delta)

				switch be.EventAction {
				case "vin_spend":
					totalVinSpend.Add(totalVinSpend, new(big.Int).Abs(new(big.Int).Set(delta)))
				case "vout_receive":
					totalVoutReceive.Add(totalVoutReceive, delta)
				}
			}

			for _, expectedPath := range tc.expectedEventPaths {
				_, exists := seenPaths[expectedPath]
				assert.True(t, exists, "missing expected btc event_path %q", expectedPath)
			}
			assert.Equal(t, tc.expectedNetDelta, netDelta.String())

			if tc.expectFeeConserves {
				fee := parseDecimal(t, tx.FeeAmount)
				inputMinusOutput := big.NewInt(0).Sub(totalVinSpend, totalVoutReceive)
				assert.Equal(t, fee.String(), inputMinusOutput.String())
			}
		})
	}
}

func TestProcessBatch_BTCTopologyParityLikeGroupVsIndependent_CanonicalTupleEquivalence(t *testing.T) {
	type btcFixture struct {
		signature string
		sequence  int64
		txHash    string
		vinDelta  string
		voutDelta string
	}
	type topologyResult struct {
		tupleSet  map[string]struct{}
		eventIDs  map[string]struct{}
		finalHead string
	}

	fixtures := []btcFixture{
		{
			signature: "0xABCD9901",
			sequence:  9901,
			txHash:    "ABCD9901",
			vinDelta:  "-3000",
			voutDelta: "2900",
		},
		{
			signature: "ABCD9902",
			sequence:  9902,
			txHash:    "0xabcd9902",
			vinDelta:  "-1200",
			voutDelta: "1100",
		},
	}

	buildResult := func(f btcFixture) *sidecarv1.TransactionResult {
		return &sidecarv1.TransactionResult{
			TxHash:      f.txHash,
			BlockCursor: f.sequence,
			FeeAmount:   "100",
			FeePayer:    "tb1-topology-owner",
			Status:      "SUCCESS",
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					OuterInstructionIndex: 0,
					InnerInstructionIndex: -1,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "vin_spend",
					ProgramId:             "btc",
					Address:               "tb1-topology-owner",
					ContractAddress:       "BTC",
					Delta:                 f.vinDelta,
					TokenSymbol:           "BTC",
					TokenName:             "Bitcoin",
					TokenDecimals:         8,
					TokenType:             string(model.TokenTypeNative),
					Metadata: map[string]string{
						"event_path":     "vin:0",
						"finality_state": "confirmed",
					},
				},
				{
					OuterInstructionIndex: 1,
					InnerInstructionIndex: -1,
					EventCategory:         string(model.EventCategoryTransfer),
					EventAction:           "vout_receive",
					ProgramId:             "btc",
					Address:               "tb1-topology-owner",
					ContractAddress:       "BTC",
					Delta:                 f.voutDelta,
					TokenSymbol:           "BTC",
					TokenName:             "Bitcoin",
					TokenDecimals:         8,
					TokenType:             string(model.TokenTypeNative),
					Metadata: map[string]string{
						"event_path":     "vout:1",
						"finality_state": "confirmed",
					},
				},
			},
		}
	}

	runTopology := func(t *testing.T, mode string, partitions [][]btcFixture) topologyResult {
		t.Helper()

		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)
		normalizedCh := make(chan event.NormalizedBatch, len(partitions))
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		call := 0
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(len(partitions)).
			DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
				expectedPartition := partitions[call]
				require.Len(t, req.GetTransactions(), len(expectedPartition))

				results := make([]*sidecarv1.TransactionResult, 0, len(expectedPartition))
				for idx, f := range expectedPartition {
					assert.Equal(
						t,
						canonicalSignatureIdentity(model.ChainBTC, f.signature),
						canonicalSignatureIdentity(model.ChainBTC, req.GetTransactions()[idx].GetSignature()),
					)
					results = append(results, buildResult(f))
				}
				call++
				return &sidecarv1.DecodeSolanaTransactionBatchResponse{Results: results}, nil
			})

		tupleSet := make(map[string]struct{}, 8)
		eventIDs := make(map[string]struct{}, 8)
		prevCursor := strPtr("0xABCD9900")
		prevSequence := int64(9900)
		finalCursor := ""

		for _, partition := range partitions {
			rawTxs := make([]json.RawMessage, len(partition))
			signatures := make([]event.SignatureInfo, 0, len(partition))
			for i, f := range partition {
				rawTxs[i] = json.RawMessage(`{"tx":"btc-topology-parity-proof"}`)
				signatures = append(signatures, event.SignatureInfo{
					Hash:     f.signature,
					Sequence: f.sequence,
				})
			}

			nextCursor := partition[len(partition)-1].signature
			batch := event.RawBatch{
				Chain:                  model.ChainBTC,
				Network:                model.NetworkTestnet,
				Address:                "tb1-topology-owner",
				PreviousCursorValue:    prevCursor,
				PreviousCursorSequence: prevSequence,
				NewCursorValue:         &nextCursor,
				NewCursorSequence:      partition[len(partition)-1].sequence,
				RawTransactions:        rawTxs,
				Signatures:             signatures,
			}

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			normalized := <-normalizedCh
			for _, tuple := range orderedCanonicalTuples(normalized) {
				key := strings.Join([]string{tuple.EventID, tuple.TxHash, tuple.EventCategory, tuple.Delta}, "|")
				tupleSet[key] = struct{}{}

				_, exists := eventIDs[tuple.EventID]
				require.False(t, exists, "duplicate canonical event id in %s topology run: %s", mode, tuple.EventID)
				eventIDs[tuple.EventID] = struct{}{}
			}

			require.NotNil(t, normalized.NewCursorValue)
			finalCursor = *normalized.NewCursorValue
			prevCursor = normalized.NewCursorValue
			prevSequence = normalized.NewCursorSequence
		}

		return topologyResult{
			tupleSet:  tupleSet,
			eventIDs:  eventIDs,
			finalHead: finalCursor,
		}
	}

	likeGroup := runTopology(t, "like-group", [][]btcFixture{
		{fixtures[0], fixtures[1]},
	})
	independent := runTopology(t, "independent", [][]btcFixture{
		{fixtures[0]},
		{fixtures[1]},
	})

	assert.Equal(t, likeGroup.tupleSet, independent.tupleSet, "btc-testnet topology modes must converge to canonical tuple equivalence")
	assert.Equal(t, likeGroup.eventIDs, independent.eventIDs, "btc-testnet topology modes must converge to identical canonical event id set")
	assert.Equal(t, "abcd9902", likeGroup.finalHead)
	assert.Equal(t, likeGroup.finalHead, independent.finalHead)
}

func TestProcessBatch_MandatoryChainTopologyABCParityReplayResume_CanonicalIDAndCursorInvariants(t *testing.T) {
	type topologyFixture struct {
		signature          string
		topologyCSignature string
		sequence           int64
		result             *sidecarv1.TransactionResult
	}
	type testCase struct {
		name          string
		chain         model.Chain
		network       model.Network
		address       string
		initialCursor string
		fixtures      []topologyFixture
		expectedHead  string
	}
	type topologyRun struct {
		tupleSetWithMode   map[string]struct{}
		tupleSetLogical    map[string]struct{}
		eventIDSet        map[string]struct{}
		finalHead         string
	}

	signatureForMode := func(mode string, fixture topologyFixture) string {
		if mode == "C" {
			return fixture.topologyCSignature
		}
		return fixture.signature
	}
	eventIDSet := func(batch event.NormalizedBatch) map[string]struct{} {
		out := make(map[string]struct{})
		for _, tx := range batch.Transactions {
			for _, be := range tx.BalanceEvents {
				out[be.EventID] = struct{}{}
			}
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
				require.False(t, exists, "duplicate canonical event id in batch: %s", be.EventID)
				seen[be.EventID] = struct{}{}
			}
		}
	}
	canonicalTupleInventoryKey := func(chain model.Chain, network model.Network, mode string) string {
		return strings.Join([]string{string(chain), string(network), mode}, "|")
	}
	canonicalTupleKeyNoMode := func(batch event.NormalizedBatch, tx event.NormalizedTransaction, be event.NormalizedBalanceEvent) string {
		return strings.Join([]string{
			string(batch.Chain),
			string(batch.Network),
			strconv.FormatInt(tx.BlockCursor, 10),
			tx.TxHash,
			be.EventPath,
			be.ActorAddress,
			be.AssetID,
			string(be.EventCategory),
		}, "|")
	}
	canonicalTupleKey := func(mode string, batch event.NormalizedBatch, tx event.NormalizedTransaction, be event.NormalizedBalanceEvent) string {
		return strings.Join([]string{
			string(batch.Chain),
			string(batch.Network),
			mode,
			strconv.FormatInt(tx.BlockCursor, 10),
			tx.TxHash,
			be.EventPath,
			be.ActorAddress,
			be.AssetID,
			string(be.EventCategory),
		}, "|")
	}
	topologyModes := []string{"A", "B", "C"}
	topologyPartitions := map[string][][]int{
		"A": {{0, 1}},
		"B": {{0}, {1}},
		"C": {{0}, {1}},
	}

	testCases := []testCase{
		{
			name:          "solana-devnet",
			chain:         model.ChainSolana,
			network:       model.NetworkDevnet,
			address:       "sol-topology-owner",
			initialCursor: "sol-topology-7100",
			expectedHead:  "sol-topology-7102",
			fixtures: []topologyFixture{
				{
					signature:          "sol-topology-7101",
					topologyCSignature: "  sol-topology-7101  ",
					sequence:           7101,
					result: &sidecarv1.TransactionResult{
						TxHash:      "sol-topology-7101",
						BlockCursor: 7101,
						FeeAmount:   "5000",
						FeePayer:    "sol-topology-owner",
						Status:      "SUCCESS",
						BalanceEvents: []*sidecarv1.BalanceEventInfo{
							{
								OuterInstructionIndex: 0,
								InnerInstructionIndex: -1,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "transfer",
								ProgramId:             "11111111111111111111111111111111",
								Address:               "sol-topology-owner",
								ContractAddress:       "So11111111111111111111111111111111111111112",
								Delta:                 "-7",
								TokenSymbol:           "SOL",
								TokenName:             "Solana",
								TokenDecimals:         9,
								TokenType:             string(model.TokenTypeNative),
								Metadata: map[string]string{
									"event_path":     "outer:0|inner:-1",
									"finality_state": "confirmed",
								},
							},
						},
					},
				},
				{
					signature:          "sol-topology-7102",
					topologyCSignature: "sol-topology-7102   ",
					sequence:           7102,
					result: &sidecarv1.TransactionResult{
						TxHash:      "sol-topology-7102",
						BlockCursor: 7102,
						FeeAmount:   "4000",
						FeePayer:    "sol-topology-owner",
						Status:      "SUCCESS",
						BalanceEvents: []*sidecarv1.BalanceEventInfo{
							{
								OuterInstructionIndex: 1,
								InnerInstructionIndex: -1,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "transfer",
								ProgramId:             "11111111111111111111111111111111",
								Address:               "sol-topology-owner",
								ContractAddress:       "So11111111111111111111111111111111111111112",
								Delta:                 "3",
								TokenSymbol:           "SOL",
								TokenName:             "Solana",
								TokenDecimals:         9,
								TokenType:             string(model.TokenTypeNative),
								Metadata: map[string]string{
									"event_path":     "outer:1|inner:-1",
									"finality_state": "confirmed",
								},
							},
						},
					},
				},
			},
		},
		{
			name:          "base-sepolia",
			chain:         model.ChainBase,
			network:       model.NetworkSepolia,
			address:       "0xabcdef00000000000000000000000000007700",
			initialCursor: "0xABCD7700",
			expectedHead:  "0xabcd7702",
			fixtures: []topologyFixture{
				{
					signature:          "0xABCD7701",
					topologyCSignature: "ABCD7701",
					sequence:           7701,
					result: &sidecarv1.TransactionResult{
						TxHash:      "ABCD7701",
						BlockCursor: 7701,
						FeeAmount:   "100",
						FeePayer:    "0xabcdef00000000000000000000000000007700",
						Status:      "SUCCESS",
						BalanceEvents: []*sidecarv1.BalanceEventInfo{
							{
								OuterInstructionIndex: 10,
								InnerInstructionIndex: 0,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "transfer",
								ProgramId:             "0xabcdef00000000000000000000000000007701",
								Address:               "0xabcdef00000000000000000000000000007700",
								ContractAddress:       "0xabcdef00000000000000000000000000007701",
								Delta:                 "-6",
								CounterpartyAddress:   "0xabcdef00000000000000000000000000007711",
								TokenDecimals:         18,
								TokenType:             string(model.TokenTypeFungible),
								Metadata: map[string]string{
									"event_path":       "log:10",
									"fee_execution_l2": "70",
									"fee_data_l1":      "30",
								},
							},
						},
					},
				},
				{
					signature:          "abcd7702",
					topologyCSignature: "0XABCD7702",
					sequence:           7702,
					result: &sidecarv1.TransactionResult{
						TxHash:      "0xabcd7702",
						BlockCursor: 7702,
						FeeAmount:   "100",
						FeePayer:    "0xabcdef00000000000000000000000000007700",
						Status:      "SUCCESS",
						BalanceEvents: []*sidecarv1.BalanceEventInfo{
							{
								OuterInstructionIndex: 11,
								InnerInstructionIndex: 0,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "transfer",
								ProgramId:             "0xabcdef00000000000000000000000000007701",
								Address:               "0xabcdef00000000000000000000000000007700",
								ContractAddress:       "0xabcdef00000000000000000000000000007701",
								Delta:                 "2",
								CounterpartyAddress:   "0xabcdef00000000000000000000000000007722",
								TokenDecimals:         18,
								TokenType:             string(model.TokenTypeFungible),
								Metadata: map[string]string{
									"event_path":       "log:11",
									"fee_execution_l2": "70",
									"fee_data_l1":      "30",
								},
							},
						},
					},
				},
			},
		},
		{
			name:          "btc-testnet",
			chain:         model.ChainBTC,
			network:       model.NetworkTestnet,
			address:       "tb1-topology-owner",
			initialCursor: "0xBCDD8800",
			expectedHead:  "bcdd8802",
			fixtures: []topologyFixture{
				{
					signature:          "0xBCDD8801",
					topologyCSignature: "BCDD8801",
					sequence:           8801,
					result: &sidecarv1.TransactionResult{
						TxHash:      "BCDD8801",
						BlockCursor: 8801,
						FeeAmount:   "100",
						FeePayer:    "tb1-topology-owner",
						Status:      "SUCCESS",
						BalanceEvents: []*sidecarv1.BalanceEventInfo{
							{
								OuterInstructionIndex: 0,
								InnerInstructionIndex: -1,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "vin_spend",
								ProgramId:             "btc",
								Address:               "tb1-topology-owner",
								ContractAddress:       "BTC",
								Delta:                 "-900",
								TokenSymbol:           "BTC",
								TokenName:             "Bitcoin",
								TokenDecimals:         8,
								TokenType:             string(model.TokenTypeNative),
								Metadata: map[string]string{
									"event_path":     "vin:0",
									"finality_state": "confirmed",
								},
							},
							{
								OuterInstructionIndex: 1,
								InnerInstructionIndex: -1,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "vout_receive",
								ProgramId:             "btc",
								Address:               "tb1-topology-owner",
								ContractAddress:       "BTC",
								Delta:                 "800",
								TokenSymbol:           "BTC",
								TokenName:             "Bitcoin",
								TokenDecimals:         8,
								TokenType:             string(model.TokenTypeNative),
								Metadata: map[string]string{
									"event_path":     "vout:1",
									"finality_state": "confirmed",
								},
							},
						},
					},
				},
				{
					signature:          "bcdd8802",
					topologyCSignature: "0XBCDD8802",
					sequence:           8802,
					result: &sidecarv1.TransactionResult{
						TxHash:      "0xbcdd8802",
						BlockCursor: 8802,
						FeeAmount:   "100",
						FeePayer:    "tb1-topology-owner",
						Status:      "SUCCESS",
						BalanceEvents: []*sidecarv1.BalanceEventInfo{
							{
								OuterInstructionIndex: 0,
								InnerInstructionIndex: -1,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "vin_spend",
								ProgramId:             "btc",
								Address:               "tb1-topology-owner",
								ContractAddress:       "BTC",
								Delta:                 "-700",
								TokenSymbol:           "BTC",
								TokenName:             "Bitcoin",
								TokenDecimals:         8,
								TokenType:             string(model.TokenTypeNative),
								Metadata: map[string]string{
									"event_path":     "vin:0",
									"finality_state": "confirmed",
								},
							},
							{
								OuterInstructionIndex: 1,
								InnerInstructionIndex: -1,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "vout_receive",
								ProgramId:             "btc",
								Address:               "tb1-topology-owner",
								ContractAddress:       "BTC",
								Delta:                 "600",
								TokenSymbol:           "BTC",
								TokenName:             "Bitcoin",
								TokenDecimals:         8,
								TokenType:             string(model.TokenTypeNative),
								Metadata: map[string]string{
									"event_path":     "vout:1",
									"finality_state": "confirmed",
								},
							},
						},
					},
				},
			},
		},
	}

	runTopology := func(t *testing.T, tc testCase, mode string) topologyRun {
		t.Helper()

		partitions := topologyPartitions[mode]
		require.NotEmpty(t, partitions)

		ctrl := gomock.NewController(t)
		mockClient := mocks.NewMockChainDecoderClient(ctrl)
		normalizedCh := make(chan event.NormalizedBatch, len(partitions)+1)
		n := &Normalizer{
			sidecarTimeout: 30 * time.Second,
			normalizedCh:   normalizedCh,
			logger:         slog.Default(),
		}

		callPartitions := append(make([][]int, 0, len(partitions)+1), partitions...)
		callPartitions = append(callPartitions, partitions[len(partitions)-1]) // replay of final topology boundary
		call := 0
		mockClient.EXPECT().
			DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(len(callPartitions)).
			DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
				expectedPartition := callPartitions[call]
				require.Len(t, req.GetTransactions(), len(expectedPartition))

				results := make([]*sidecarv1.TransactionResult, 0, len(expectedPartition))
				for idx, fixtureIdx := range expectedPartition {
					expectedSignature := signatureForMode(mode, tc.fixtures[fixtureIdx])
					assert.Equal(
						t,
						canonicalSignatureIdentity(tc.chain, expectedSignature),
						canonicalSignatureIdentity(tc.chain, req.GetTransactions()[idx].GetSignature()),
					)
					results = append(results, tc.fixtures[fixtureIdx].result)
				}
				call++
				return &sidecarv1.DecodeSolanaTransactionBatchResponse{Results: results}, nil
			})

		tupleSetWithMode := make(map[string]struct{}, 16)
		tupleSetLogical := make(map[string]struct{}, 16)
		eventIDs := make(map[string]struct{}, 16)
		prevCursor := strPtr(tc.initialCursor)
		prevSequence := tc.fixtures[0].sequence - 1
		finalHead := ""

		var lastPartition event.NormalizedBatch
		for _, partition := range partitions {
			rawTxs := make([]json.RawMessage, 0, len(partition))
			signatures := make([]event.SignatureInfo, 0, len(partition))
			for _, fixtureIdx := range partition {
				signature := signatureForMode(mode, tc.fixtures[fixtureIdx])
				rawTxs = append(rawTxs, json.RawMessage(`{"tx":"mandatory-topology-proof"}`))
				signatures = append(signatures, event.SignatureInfo{
					Hash:     signature,
					Sequence: tc.fixtures[fixtureIdx].sequence,
				})
			}

			lastFixture := tc.fixtures[partition[len(partition)-1]]
			nextCursor := signatureForMode(mode, lastFixture)
			batch := event.RawBatch{
				Chain:                  tc.chain,
				Network:                tc.network,
				Address:                tc.address,
				PreviousCursorValue:    prevCursor,
				PreviousCursorSequence: prevSequence,
				NewCursorValue:         &nextCursor,
				NewCursorSequence:      lastFixture.sequence,
				RawTransactions:        rawTxs,
				Signatures:             signatures,
			}

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, batch))
			normalized := <-normalizedCh
			assertNoDuplicateCanonicalIDs(t, normalized)
			assert.GreaterOrEqual(t, normalized.NewCursorSequence, normalized.PreviousCursorSequence)

			for _, tx := range normalized.Transactions {
				for _, be := range tx.BalanceEvents {
					_, exists := eventIDs[be.EventID]
					require.False(t, exists, "duplicate canonical event id in topology %s: %s", mode, be.EventID)
					eventIDs[be.EventID] = struct{}{}
					tupleSetWithMode[canonicalTupleKey(mode, normalized, tx, be)] = struct{}{}
					tupleSetLogical[canonicalTupleKeyNoMode(normalized, tx, be)] = struct{}{}
				}
			}

			lastPartition = normalized
			require.NotNil(t, normalized.NewCursorValue)
			finalHead = *normalized.NewCursorValue
			prevCursor = normalized.NewCursorValue
			prevSequence = normalized.NewCursorSequence
		}

		replayPartition := partitions[len(partitions)-1]
		replayRaw := make([]json.RawMessage, 0, len(replayPartition))
		replaySigs := make([]event.SignatureInfo, 0, len(replayPartition))
		for _, fixtureIdx := range replayPartition {
			signature := signatureForMode(mode, tc.fixtures[fixtureIdx])
			replayRaw = append(replayRaw, json.RawMessage(`{"tx":"mandatory-topology-proof-replay"}`))
			replaySigs = append(replaySigs, event.SignatureInfo{
				Hash:     signature,
				Sequence: tc.fixtures[fixtureIdx].sequence,
			})
		}

		replayBatch := event.RawBatch{
			Chain:                  tc.chain,
			Network:                tc.network,
			Address:                tc.address,
			PreviousCursorValue:    prevCursor,
			PreviousCursorSequence: prevSequence,
			NewCursorValue:         prevCursor,
			NewCursorSequence:      prevSequence,
			RawTransactions:        replayRaw,
			Signatures:             replaySigs,
		}
		require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockClient, replayBatch))
		replayRun := <-normalizedCh
		assertNoDuplicateCanonicalIDs(t, replayRun)
		assert.Equal(t, orderedCanonicalTuples(lastPartition), orderedCanonicalTuples(replayRun), "replay/resume must be idempotent at topology boundary")
		assert.Equal(t, eventIDSet(lastPartition), eventIDSet(replayRun), "replay/resume canonical event ids drifted at topology boundary")
		assert.GreaterOrEqual(t, replayRun.NewCursorSequence, replayRun.PreviousCursorSequence)
		assert.GreaterOrEqual(t, replayRun.NewCursorSequence, lastPartition.NewCursorSequence)

		return topologyRun{
			tupleSetWithMode: tupleSetWithMode,
			tupleSetLogical:  tupleSetLogical,
			eventIDSet:      eventIDs,
			finalHead:       finalHead,
		}
	}

	requiredTopologyInventory := make(map[string]struct{}, len(testCases)*len(topologyModes))
	for _, tc := range testCases {
		for _, mode := range topologyModes {
			requiredTopologyInventory[canonicalTupleInventoryKey(tc.chain, tc.network, mode)] = struct{}{}
		}
	}
	observedTopologyInventory := make(map[string]struct{}, len(testCases)*len(topologyPartitions))

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			topologyA := runTopology(t, tc, "A")
			topologyB := runTopology(t, tc, "B")
			topologyC := runTopology(t, tc, "C")

			observedTopologyInventory[canonicalTupleInventoryKey(tc.chain, tc.network, "A")] = struct{}{}
			observedTopologyInventory[canonicalTupleInventoryKey(tc.chain, tc.network, "B")] = struct{}{}
			observedTopologyInventory[canonicalTupleInventoryKey(tc.chain, tc.network, "C")] = struct{}{}

			assert.Equal(t, topologyA.tupleSetLogical, topologyB.tupleSetLogical, "topology A/B must converge to one canonical tuple set")
			assert.Equal(t, topologyA.tupleSetLogical, topologyC.tupleSetLogical, "topology A/C must converge to one canonical tuple set")
			assert.Equal(t, topologyA.eventIDSet, topologyB.eventIDSet, "topology A/B canonical event id set must match")
			assert.Equal(t, topologyA.eventIDSet, topologyC.eventIDSet, "topology A/C canonical event id set must match")

			assert.Equal(t, tc.expectedHead, topologyA.finalHead)
			assert.Equal(t, tc.expectedHead, topologyB.finalHead)
			assert.Equal(t, tc.expectedHead, topologyC.finalHead)
		})
	}

	missingInventory := make([]string, 0)
	for cell := range requiredTopologyInventory {
		if _, ok := observedTopologyInventory[cell]; !ok {
			missingInventory = append(missingInventory, cell)
		}
	}
	require.Empty(t, missingInventory, "mandatory topology parity inventory is incomplete: %v", missingInventory)
}

func TestNormalizedTxFromResult_BTCUsesUTXOCanonicalizationWithoutSyntheticFeeEvent(t *testing.T) {
	n := &Normalizer{}
	batch := event.RawBatch{
		Chain:   model.ChainBTC,
		Network: model.NetworkTestnet,
	}
	result := &sidecarv1.TransactionResult{
		TxHash:      "ABCDEF001122",
		BlockCursor: 321,
		BlockTime:   1_700_000_321,
		FeeAmount:   "100",
		FeePayer:    "tb1payer",
		Status:      "SUCCESS",
		BalanceEvents: []*sidecarv1.BalanceEventInfo{
			{
				OuterInstructionIndex: 0,
				InnerInstructionIndex: -1,
				EventCategory:         "TRANSFER",
				EventAction:           "vin_spend",
				ProgramId:             "btc",
				Address:               "tb1payer",
				ContractAddress:       "BTC",
				Delta:                 "-10000",
				TokenSymbol:           "BTC",
				TokenName:             "Bitcoin",
				TokenDecimals:         8,
				TokenType:             "NATIVE",
				Metadata: map[string]string{
					"event_path":     "vin:0",
					"finality_state": "confirmed",
				},
			},
		},
	}

	tx := n.normalizedTxFromResult(batch, result)
	require.Len(t, tx.BalanceEvents, 1)
	assert.Equal(t, "abcdef001122", tx.TxHash)

	be := tx.BalanceEvents[0]
	assert.Equal(t, "vin:0", be.EventPath)
	assert.Equal(t, "btc_utxo", be.EventPathType)
	assert.Equal(t, "btc-decoder-v1", be.DecoderVersion)
	assert.Equal(t, model.EventCategoryTransfer, be.EventCategory)
	assert.NotEmpty(t, be.EventID)
}
