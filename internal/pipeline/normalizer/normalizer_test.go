package normalizer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/normalizer/mocks"
	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
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
