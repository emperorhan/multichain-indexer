package ingester

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// ---------------------------------------------------------------------------
// Helper: build a minimal batch with balance events for processBatch tests.
// ---------------------------------------------------------------------------

func makeBasicBatch(chain model.Chain, network model.Network, address string, newSeq int64, txs []event.NormalizedTransaction) event.NormalizedBatch {
	walletID := "wallet-" + address
	orgID := "org-" + address
	cursorVal := fmt.Sprintf("cursor-%d", newSeq)
	return event.NormalizedBatch{
		Chain:             chain,
		Network:           network,
		Address:           address,
		WalletID:          &walletID,
		OrgID:             &orgID,
		NewCursorValue:    &cursorVal,
		NewCursorSequence: newSeq,
		Transactions:      txs,
	}
}

func makeNormalizedBalanceEvent(address, contractAddress, delta string, tokenType model.TokenType, activity model.ActivityType) event.NormalizedBalanceEvent {
	return event.NormalizedBalanceEvent{
		OuterInstructionIndex: 0,
		InnerInstructionIndex: -1,
		ActivityType:          activity,
		EventAction:           "transfer",
		ProgramID:             "test",
		ContractAddress:       contractAddress,
		Address:               address,
		Delta:                 delta,
		TokenSymbol:           "TEST",
		TokenName:             "Test Token",
		TokenDecimals:         9,
		TokenType:             tokenType,
		EventID:               uuid.NewString(),
		EventPathType:         "test",
		DecoderVersion:        "test-v1",
		FinalityState:         "confirmed",
	}
}

// newCustomMocks creates gomock-based mocks without any pre-configured expectations.
func newCustomMocks(t *testing.T) (
	*gomock.Controller,
	*storemocks.MockTxBeginner,
	*storemocks.MockTransactionRepository,
	*storemocks.MockBalanceEventRepository,
	*storemocks.MockBalanceRepository,
	*storemocks.MockTokenRepository,
	*storemocks.MockWatermarkRepository,
) {
	ctrl := gomock.NewController(t)
	return ctrl,
		storemocks.NewMockTxBeginner(ctrl),
		storemocks.NewMockTransactionRepository(ctrl),
		storemocks.NewMockBalanceEventRepository(ctrl),
		storemocks.NewMockBalanceRepository(ctrl),
		storemocks.NewMockTokenRepository(ctrl),
		storemocks.NewMockWatermarkRepository(ctrl)
}

// setupStandardTxAndToken sets up BulkUpsertTx for tx and token repos, returning fixed IDs.
func setupStandardTxAndToken(
	mockTxRepo *storemocks.MockTransactionRepository,
	mockTokenRepo *storemocks.MockTokenRepository,
	txID, tokenID uuid.UUID,
) {
	mockTxRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, txn := range txns {
				result[txn.TxHash] = txID
			}
			return result, nil
		})
	mockTokenRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(tokens))
			for _, tok := range tokens {
				result[tok.ContractAddress] = tokenID
			}
			return result, nil
		})
}

// ---------------------------------------------------------------------------
// Test: buildEventModels — scam detection path (zero_balance_withdrawal)
// ---------------------------------------------------------------------------

func TestBuildEventModels_ScamDetection_SkipsAndDeniesToken(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default())

	address := "addr-scam-test"
	contractAddress := "SCAM_TOKEN"
	now := time.Now()

	// Negative delta for a non-native token where balance does NOT exist
	// triggers "zero_balance_withdrawal" scam signal.
	be := makeNormalizedBalanceEvent(address, contractAddress, "-1000", model.TokenTypeFungible, model.ActivityWithdrawal)

	batch := makeBasicBatch(model.ChainBase, model.NetworkSepolia, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "scam-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)

	txID := uuid.New()
	tokenID := uuid.New()

	setupBeginTx(mockDB)
	setupStandardTxAndToken(mockTxRepo, mockTokenRepo, txID, tokenID)

	// All tokens are NOT denied initially
	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	// Return Exists=false to trigger scam detection
	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "0", Exists: false}
			}
			return result, nil
		})

	// DenyTokenTx should be called for the scam token
	mockTokenRepo.EXPECT().
		DenyTokenTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, contractAddress, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	// No balance events to write (scam detected => skipped), watermark still updated
	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, int64(100)).
		Return(nil)

	require.NoError(t, ing.processBatch(context.Background(), batch))
}

// ---------------------------------------------------------------------------
// Test: buildEventModels — scam signal from chain data metadata
// ---------------------------------------------------------------------------

func TestBuildEventModels_ScamSignalFromChainData(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default())

	address := "addr-scam-metadata"
	contractAddress := "SCAM_META_TOKEN"
	now := time.Now()

	be := makeNormalizedBalanceEvent(address, contractAddress, "1000", model.TokenTypeFungible, model.ActivityDeposit)
	be.ChainData = json.RawMessage(`{"scam_signal":"honeypot_detected"}`)

	batch := makeBasicBatch(model.ChainEthereum, model.NetworkMainnet, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "scam-meta-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)

	txID := uuid.New()
	tokenID := uuid.New()

	setupBeginTx(mockDB)
	setupStandardTxAndToken(mockTxRepo, mockTokenRepo, txID, tokenID)

	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "999999999999999999999", Exists: true}
			}
			return result, nil
		})

	// DenyTokenTx should be called for the scam signal detected from chain data
	mockTokenRepo.EXPECT().
		DenyTokenTx(gomock.Any(), gomock.Any(), model.ChainEthereum, model.NetworkMainnet, contractAddress, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainEthereum, model.NetworkMainnet, int64(100)).
		Return(nil)

	require.NoError(t, ing.processBatch(context.Background(), batch))
}

// ---------------------------------------------------------------------------
// Test: buildEventModels — negative balance path
// ---------------------------------------------------------------------------

func TestBuildEventModels_NegativeBalance_SkipsBalanceApplication(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default())

	address := "addr-neg-bal"
	contractAddress := "SOL"
	now := time.Now()

	// Delta -200 with balance 100 => negative
	be := makeNormalizedBalanceEvent(address, contractAddress, "-200", model.TokenTypeNative, model.ActivityWithdrawal)

	batch := makeBasicBatch(model.ChainSolana, model.NetworkDevnet, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "neg-bal-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)

	txID := uuid.New()
	tokenID := uuid.New()

	setupBeginTx(mockDB)
	setupStandardTxAndToken(mockTxRepo, mockTokenRepo, txID, tokenID)

	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	// Return a small balance so delta (-200) results in negative
	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "100", Exists: true}
			}
			return result, nil
		})

	var capturedEvents []*model.BalanceEvent
	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			capturedEvents = append(capturedEvents, events...)
			return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
		})

	// No adjustments (negative balance → BalanceApplied=false)
	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, int64(100)).
		Return(nil)

	require.NoError(t, ing.processBatch(context.Background(), batch))

	require.Len(t, capturedEvents, 1)
	assert.False(t, capturedEvents[0].BalanceApplied, "negative balance should result in BalanceApplied=false")
}

// ---------------------------------------------------------------------------
// Test: buildEventModels — self-transfer detection
// ---------------------------------------------------------------------------

func TestBuildEventModels_SelfTransfer_DetectedAndLogged(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default())

	address := "addr-self-transfer"
	contractAddress := "SOL"
	now := time.Now()

	be := makeNormalizedBalanceEvent(address, contractAddress, "-1000", model.TokenTypeNative, model.ActivitySelfTransfer)
	be.CounterpartyAddress = address // self-transfer

	batch := makeBasicBatch(model.ChainSolana, model.NetworkDevnet, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "self-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)

	txID := uuid.New()
	tokenID := uuid.New()

	setupBeginTx(mockDB)
	setupStandardTxAndToken(mockTxRepo, mockTokenRepo, txID, tokenID)

	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "999999999999999999999", Exists: true}
			}
			return result, nil
		})

	var capturedEvents []*model.BalanceEvent
	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			capturedEvents = append(capturedEvents, events...)
			return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
		})

	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, int64(100)).
		Return(nil)

	require.NoError(t, ing.processBatch(context.Background(), batch))

	require.Len(t, capturedEvents, 1)
	assert.Equal(t, address, capturedEvents[0].Address)
	assert.Equal(t, address, capturedEvents[0].CounterpartyAddress)
}

// ---------------------------------------------------------------------------
// Test: buildEventModels — staking activity aggregation
// ---------------------------------------------------------------------------

func TestBuildEventModels_StakingActivity_ProducesStakedDelta(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default())

	address := "addr-staking"
	contractAddress := "SOL"
	now := time.Now()

	be := makeNormalizedBalanceEvent(address, contractAddress, "-5000", model.TokenTypeNative, model.ActivityStake)

	batch := makeBasicBatch(model.ChainSolana, model.NetworkDevnet, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "stake-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)

	txID := uuid.New()
	tokenID := uuid.New()

	setupBeginTx(mockDB)
	setupStandardTxAndToken(mockTxRepo, mockTokenRepo, txID, tokenID)

	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "999999999999999999999", Exists: true}
			}
			return result, nil
		})

	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
		})

	// Expect 2 adjustment items: liquid + staked
	var capturedAdjustments []store.BulkAdjustItem
	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, items []store.BulkAdjustItem) error {
			capturedAdjustments = append(capturedAdjustments, items...)
			return nil
		})

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, int64(100)).
		Return(nil)

	require.NoError(t, ing.processBatch(context.Background(), batch))

	require.Len(t, capturedAdjustments, 2, "staking activity should produce 2 adjustments: liquid + staked")

	var liquidItem, stakedItem store.BulkAdjustItem
	for _, item := range capturedAdjustments {
		if item.BalanceType == "staked" {
			stakedItem = item
		} else {
			liquidItem = item
		}
	}
	assert.Equal(t, "-5000", liquidItem.Delta)
	assert.Equal(t, "", liquidItem.BalanceType)
	assert.Equal(t, "5000", stakedItem.Delta)
	assert.Equal(t, "staked", stakedItem.BalanceType)
}

// ---------------------------------------------------------------------------
// Test: buildEventModels — block scan mode with blockScanAddrMap
// ---------------------------------------------------------------------------

func TestBuildEventModels_BlockScanMode_ResolvesFromAddrMap(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	mockWatchedAddrRepo := storemocks.NewMockWatchedAddressRepository(ctrl)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default(),
		WithWatchedAddressRepo(mockWatchedAddrRepo),
	)

	address := "0xBlockScanAddr1"
	counterparty := "0xCounterparty1"
	contractAddress := "ETH"
	now := time.Now()
	walletID := "wallet-bs"
	orgID := "org-bs"

	be := makeNormalizedBalanceEvent(address, contractAddress, "1000", model.TokenTypeNative, model.ActivityDeposit)
	be.CounterpartyAddress = counterparty

	batch := makeBasicBatch(model.ChainBase, model.NetworkSepolia, "", 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "bs-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: counterparty, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)
	batch.BlockScanMode = true
	batch.WatchedAddresses = []string{address}

	txID := uuid.New()
	tokenID := uuid.New()

	fakeDB := openRFFakeDB(t)
	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
			return fakeDB.BeginTx(ctx, opts)
		})

	setupStandardTxAndToken(mockTxRepo, mockTokenRepo, txID, tokenID)

	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "999999999999999999999", Exists: true}
			}
			return result, nil
		})

	var capturedEvents []*model.BalanceEvent
	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			capturedEvents = append(capturedEvents, events...)
			return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
		})

	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, int64(100)).
		Return(nil)

	// Inject blockScanAddrMap cache. The lookup in buildEventModels uses the
	// raw ec.be.Address as key, so it must match exactly.
	ing.blockScanAddrCache = map[string]map[string]addrMeta{
		"base:sepolia": {
			address: {walletID: &walletID, orgID: &orgID},
		},
	}
	ing.blockScanAddrCacheAt = map[string]time.Time{
		"base:sepolia": time.Now().Add(1 * time.Hour),
	}
	ing.blockScanAddrCacheTTL = 2 * time.Hour

	require.NoError(t, ing.processBatch(context.Background(), batch))

	require.Len(t, capturedEvents, 1)
	assert.NotNil(t, capturedEvents[0].WalletID)
	assert.Equal(t, walletID, *capturedEvents[0].WalletID)
	assert.NotNil(t, capturedEvents[0].OrganizationID)
	assert.Equal(t, orgID, *capturedEvents[0].OrganizationID)
}

// ---------------------------------------------------------------------------
// Test: buildEventModels — tokenID not found continue branch
// ---------------------------------------------------------------------------

func TestBuildEventModels_MissingTokenID_SkipsEvent(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default())

	address := "addr-missing-token"
	contractAddress := "UNKNOWN_TOKEN"
	now := time.Now()

	be := makeNormalizedBalanceEvent(address, contractAddress, "1000", model.TokenTypeNative, model.ActivityDeposit)

	batch := makeBasicBatch(model.ChainSolana, model.NetworkDevnet, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "missing-token-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)

	txID := uuid.New()
	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, txn := range txns {
				result[txn.TxHash] = txID
			}
			return result, nil
		})
	// Return empty token map — token ID not found
	mockTokenRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(make(map[string]uuid.UUID), nil)

	// BulkIsDeniedTx is still called during prefetchBulkData
	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	// No events have valid tokenID, so no balance operations or events
	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, int64(100)).
		Return(nil)

	require.NoError(t, ing.processBatch(context.Background(), batch))
}

// ---------------------------------------------------------------------------
// Test: buildEventModels — block scan mode unresolved address (fallback)
// ---------------------------------------------------------------------------

func TestBuildEventModels_BlockScanMode_UnresolvedAddress_Fallback(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default())

	address := "0xUnknownAddr"
	contractAddress := "ETH"
	now := time.Now()

	be := makeNormalizedBalanceEvent(address, contractAddress, "1000", model.TokenTypeNative, model.ActivityDeposit)
	be.CounterpartyAddress = "0xAlsoUnknown"

	batch := makeBasicBatch(model.ChainBase, model.NetworkSepolia, "", 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "bs-fallback-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)
	batch.BlockScanMode = true
	batch.WatchedAddresses = []string{address}

	txID := uuid.New()
	tokenID := uuid.New()

	fakeDB := openRFFakeDB(t)
	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
			return fakeDB.BeginTx(ctx, opts)
		})

	setupStandardTxAndToken(mockTxRepo, mockTokenRepo, txID, tokenID)

	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "999999999999999999999", Exists: true}
			}
			return result, nil
		})

	var capturedEvents []*model.BalanceEvent
	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			capturedEvents = append(capturedEvents, events...)
			return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
		})

	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, int64(100)).
		Return(nil)

	require.NoError(t, ing.processBatch(context.Background(), batch))

	require.Len(t, capturedEvents, 1)
	assert.NotNil(t, capturedEvents[0].WatchedAddress)
	assert.Equal(t, address, *capturedEvents[0].WatchedAddress)
}

// ---------------------------------------------------------------------------
// Test: buildEventModels — cachedDenied path (previously denied token)
// ---------------------------------------------------------------------------

func TestBuildEventModels_CachedDenied_SkipsEvents(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default())

	address := "addr-denied-cache"
	contractDenied := "DENIED_TOKEN"
	contractAllowed := "ALLOWED_TOKEN"
	now := time.Now()

	beDenied := makeNormalizedBalanceEvent(address, contractDenied, "500", model.TokenTypeFungible, model.ActivityDeposit)
	beAllowed := makeNormalizedBalanceEvent(address, contractAllowed, "1000", model.TokenTypeNative, model.ActivityDeposit)

	batch := makeBasicBatch(model.ChainBase, model.NetworkSepolia, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "denied-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{beDenied, beAllowed},
			},
		},
	)

	txID := uuid.New()
	tokenIDDenied := uuid.New()
	tokenIDAllowed := uuid.New()

	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, txn := range txns {
				result[txn.TxHash] = txID
			}
			return result, nil
		})
	mockTokenRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(tokens))
			for _, tok := range tokens {
				if tok.ContractAddress == contractDenied {
					result[tok.ContractAddress] = tokenIDDenied
				} else {
					result[tok.ContractAddress] = tokenIDAllowed
				}
			}
			return result, nil
		})

	// Mark contractDenied as denied
	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = (a == contractDenied)
			}
			return result, nil
		})

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "999999999999999999999", Exists: true}
			}
			return result, nil
		})

	var capturedEvents []*model.BalanceEvent
	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			capturedEvents = append(capturedEvents, events...)
			return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
		})

	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, int64(100)).
		Return(nil)

	require.NoError(t, ing.processBatch(context.Background(), batch))

	// Only the allowed token's event should be persisted
	require.Len(t, capturedEvents, 1, "denied token event should be skipped")
}

// ---------------------------------------------------------------------------
// Test: writeBulkAndCommit — blockRepo path
// ---------------------------------------------------------------------------

func TestWriteBulkAndCommit_BlockRepo_UpsertsCalled(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	mockBlockRepo := storemocks.NewMockIndexedBlockRepository(ctrl)

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default(),
		WithIndexedBlockRepo(mockBlockRepo),
	)

	address := "addr-block-repo"
	contractAddress := "ETH"
	now := time.Now()

	be := makeNormalizedBalanceEvent(address, contractAddress, "1000", model.TokenTypeNative, model.ActivityDeposit)
	be.FinalityState = "confirmed"

	batch := makeBasicBatch(model.ChainEthereum, model.NetworkMainnet, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "block-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "21000", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BlockHash:     "0xblockhash123",
				ParentHash:    "0xparenthash123",
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)

	txID := uuid.New()
	tokenID := uuid.New()

	setupBeginTx(mockDB)
	setupStandardTxAndToken(mockTxRepo, mockTokenRepo, txID, tokenID)

	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "999999999999999999999", Exists: true}
			}
			return result, nil
		})

	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(store.BulkUpsertEventResult{InsertedCount: 1}, nil)

	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	var capturedBlocks []*model.IndexedBlock
	mockBlockRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, blocks []*model.IndexedBlock) error {
			capturedBlocks = append(capturedBlocks, blocks...)
			return nil
		})

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainEthereum, model.NetworkMainnet, int64(100)).
		Return(nil)

	require.NoError(t, ing.processBatch(context.Background(), batch))

	require.Len(t, capturedBlocks, 1)
	assert.Equal(t, "0xblockhash123", capturedBlocks[0].BlockHash)
	assert.Equal(t, "0xparenthash123", capturedBlocks[0].ParentHash)
	assert.Equal(t, int64(100), capturedBlocks[0].BlockNumber)
	assert.Equal(t, "confirmed", capturedBlocks[0].FinalityState)
}

// ---------------------------------------------------------------------------
// Test: writeBulkAndCommit — error paths
// ---------------------------------------------------------------------------

func TestWriteBulkAndCommit_BulkUpsertError_ReturnsError(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default())

	address := "addr-bulk-upsert-err"
	contractAddress := "SOL"
	now := time.Now()

	be := makeNormalizedBalanceEvent(address, contractAddress, "1000", model.TokenTypeNative, model.ActivityDeposit)

	batch := makeBasicBatch(model.ChainSolana, model.NetworkDevnet, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "err-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)

	txID := uuid.New()
	tokenID := uuid.New()

	setupBeginTx(mockDB)
	setupStandardTxAndToken(mockTxRepo, mockTokenRepo, txID, tokenID)

	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "999999999999999999999", Exists: true}
			}
			return result, nil
		})

	// BulkUpsertTx returns error
	bulkErr := errors.New("bulk upsert failed")
	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(store.BulkUpsertEventResult{}, bulkErr)

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bulk upsert balance events")
}

func TestWriteBulkAndCommit_BulkAdjustError_ReturnsError(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default())

	address := "addr-bulk-adjust-err"
	contractAddress := "SOL"
	now := time.Now()

	be := makeNormalizedBalanceEvent(address, contractAddress, "1000", model.TokenTypeNative, model.ActivityDeposit)

	batch := makeBasicBatch(model.ChainSolana, model.NetworkDevnet, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "adj-err-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)

	txID := uuid.New()
	tokenID := uuid.New()

	setupBeginTx(mockDB)
	setupStandardTxAndToken(mockTxRepo, mockTokenRepo, txID, tokenID)

	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "999999999999999999999", Exists: true}
			}
			return result, nil
		})

	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(store.BulkUpsertEventResult{InsertedCount: 1}, nil)

	adjustErr := errors.New("bulk adjust failed")
	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(adjustErr)

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bulk adjust balances")
}

func TestWriteBulkAndCommit_BlockRepoUpsertError_ReturnsError(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	mockBlockRepo := storemocks.NewMockIndexedBlockRepository(ctrl)

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default(),
		WithIndexedBlockRepo(mockBlockRepo),
	)

	address := "addr-block-err"
	contractAddress := "ETH"
	now := time.Now()

	be := makeNormalizedBalanceEvent(address, contractAddress, "1000", model.TokenTypeNative, model.ActivityDeposit)
	be.FinalityState = "confirmed"

	batch := makeBasicBatch(model.ChainEthereum, model.NetworkMainnet, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "block-err-tx-1", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BlockHash:     "0xblockhash",
				BalanceEvents: []event.NormalizedBalanceEvent{be},
			},
		},
	)

	txID := uuid.New()
	tokenID := uuid.New()

	setupBeginTx(mockDB)
	setupStandardTxAndToken(mockTxRepo, mockTokenRepo, txID, tokenID)

	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "999999999999999999999", Exists: true}
			}
			return result, nil
		})

	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(store.BulkUpsertEventResult{InsertedCount: 1}, nil)

	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	blockErr := errors.New("block upsert failed")
	mockBlockRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(blockErr)

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bulk upsert indexed blocks")
}

func TestWriteBulkAndCommit_WatermarkUpdateError_ReturnsError(t *testing.T) {
	ctrl, mockDB, mockTxRepo, _, _, mockTokenRepo, mockConfigRepo := newCustomMocks(t)
	_ = ctrl

	mockBERepo := storemocks.NewMockBalanceEventRepository(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)

	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, nil, slog.Default())

	address := "addr-wm-err"
	now := time.Now()

	// Batch with a transaction that has no balance events
	batch := makeBasicBatch(model.ChainSolana, model.NetworkDevnet, address, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: "wm-err-tx", BlockCursor: 100, BlockTime: &now,
				FeeAmount: "0", FeePayer: address, Status: model.TxStatusSuccess,
				ChainData: json.RawMessage("{}"),
			},
		},
	)

	txID := uuid.New()
	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, txn := range txns {
				result[txn.TxHash] = txID
			}
			return result, nil
		})
	mockTokenRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(make(map[string]uuid.UUID), nil)

	wmErr := errors.New("watermark update failed")
	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, int64(100)).
		Return(wmErr)

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update watermark")
}

// ---------------------------------------------------------------------------
// Test: rollbackCanonicalityDrift — with rollback events
// ---------------------------------------------------------------------------

func rollbackQueryRows(tokenID uuid.UUID) *rfDataRows {
	walletID := "wallet1"
	orgID := "org1"
	return &rfDataRows{
		columns: rollbackEventColumns,
		data: [][]driver.Value{
			{
				tokenID.String(), "addr1", "5000", int64(100), "tx1",
				walletID, orgID, "DEPOSIT", true,
			},
			{
				tokenID.String(), "addr1", "-3000", int64(101), "tx2",
				walletID, orgID, "WITHDRAWAL", true,
			},
		},
	}
}

func TestRollbackCanonicalityDrift_WithRollbackEvents_ReversesBalances(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	tokenID := uuid.New()

	fakeDB := openRFFakeDBWithHandler(t, func(query string, args []driver.Value) (driver.Rows, error) {
		if len(args) > 0 {
			return rollbackQueryRows(tokenID), nil
		}
		return &rfEmptyRows{}, nil
	})

	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
			return fakeDB.BeginTx(ctx, opts)
		})

	var capturedAdjustments []store.BulkAdjustItem
	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, items []store.BulkAdjustItem) error {
			capturedAdjustments = append(capturedAdjustments, items...)
			return nil
		})

	mockWmRepo.EXPECT().
		RewindWatermarkTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, gomock.Any()).
		Return(nil)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, nil, nil, mockBalanceRepo, nil, mockWmRepo, normalizedCh, slog.Default())

	prevSig := "old_sig"
	newSig := "new_sig"
	batch := event.NormalizedBatch{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		Address:                "addr1",
		PreviousCursorValue:    &prevSig,
		PreviousCursorSequence: 100,
		NewCursorValue:         &newSig,
		NewCursorSequence:      100,
	}

	err := ing.processBatch(context.Background(), batch)
	require.NoError(t, err)

	assert.NotEmpty(t, capturedAdjustments, "should have reversal adjustments")
}

func TestRollbackCanonicalityDrift_BulkAdjustError_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	tokenID := uuid.New()

	fakeDB := openRFFakeDBWithHandler(t, func(query string, args []driver.Value) (driver.Rows, error) {
		if len(args) > 0 {
			return rollbackQueryRows(tokenID), nil
		}
		return &rfEmptyRows{}, nil
	})

	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
			return fakeDB.BeginTx(ctx, opts)
		})

	adjustErr := errors.New("adjust failed during rollback")
	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, gomock.Any()).
		Return(adjustErr)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, nil, nil, mockBalanceRepo, nil, mockWmRepo, normalizedCh, slog.Default())

	prevSig := "old_sig"
	newSig := "new_sig"
	batch := event.NormalizedBatch{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		Address:                "addr1",
		PreviousCursorValue:    &prevSig,
		PreviousCursorSequence: 100,
		NewCursorValue:         &newSig,
		NewCursorSequence:      100,
	}

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "revert balances")
}

func TestRollbackCanonicalityDrift_DeleteError_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	deleteErr := errors.New("delete failed")
	fakeDB := openRFFakeDBWith(t,
		func(query string, args []driver.Value) (driver.Rows, error) {
			return &rfEmptyRows{}, nil
		},
		func(query string, args []driver.Value) (driver.Result, error) {
			return nil, deleteErr
		},
	)

	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
			return fakeDB.BeginTx(ctx, opts)
		})

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, nil, nil, mockBalanceRepo, nil, mockWmRepo, normalizedCh, slog.Default())

	prevSig := "old_sig"
	newSig := "new_sig"
	batch := event.NormalizedBatch{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		Address:                "addr1",
		PreviousCursorValue:    &prevSig,
		PreviousCursorSequence: 100,
		NewCursorValue:         &newSig,
		NewCursorSequence:      100,
	}

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete rollback balance events")
}

func TestRollbackCanonicalityDrift_RewindWatermarkError_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	fakeDB := openRFFakeDB(t)

	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
			return fakeDB.BeginTx(ctx, opts)
		})

	rewindErr := errors.New("rewind watermark failed")
	mockWmRepo.EXPECT().
		RewindWatermarkTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, gomock.Any()).
		Return(rewindErr)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, nil, nil, mockBalanceRepo, nil, mockWmRepo, normalizedCh, slog.Default())

	prevSig := "old_sig"
	newSig := "new_sig"
	batch := event.NormalizedBatch{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		Address:                "addr1",
		PreviousCursorValue:    &prevSig,
		PreviousCursorSequence: 100,
		NewCursorValue:         &newSig,
		NewCursorSequence:      100,
	}

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rewind watermark after drift")
}

func TestRollbackCanonicalityDrift_FetchRollbackEventsError_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	queryErr := errors.New("query failed")
	fakeDB := openRFFakeDBWithHandler(t, func(query string, args []driver.Value) (driver.Rows, error) {
		return nil, queryErr
	})

	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
			return fakeDB.BeginTx(ctx, opts)
		})

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, nil, nil, mockBalanceRepo, nil, mockWmRepo, normalizedCh, slog.Default())

	prevSig := "old_sig"
	newSig := "new_sig"
	batch := event.NormalizedBatch{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		Address:                "addr1",
		PreviousCursorValue:    &prevSig,
		PreviousCursorSequence: 100,
		NewCursorValue:         &newSig,
		NewCursorSequence:      100,
	}

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch rollback events")
}

// Suppress unused import warning for io.
var _ = io.EOF
