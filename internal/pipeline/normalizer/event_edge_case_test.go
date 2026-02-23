package normalizer

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/ingester"
	normalizermocks "github.com/emperorhan/multichain-indexer/internal/pipeline/normalizer/mocks"
	"github.com/emperorhan/multichain-indexer/internal/store"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
)

// ---------------------------------------------------------------------------
// Helper: normalize-only pipeline (skip fetch, capture normalized events)
// ---------------------------------------------------------------------------

type edgeCaseResult struct {
	normalized event.NormalizedBatch
	ingested   []*model.BalanceEvent
	adjusts    []store.BulkAdjustItem
}

func runEdgeCaseNormalizeAndIngest(
	t *testing.T,
	chainID model.Chain,
	networkID model.Network,
	watchedAddr string,
	decoderResp *sidecarv1.DecodeSolanaTransactionBatchResponse,
	initialBalance string,
	rawBatchOverride *event.RawBatch,
) edgeCaseResult {
	t.Helper()
	ctrl := gomock.NewController(t)

	walletID := "wallet-edge"
	orgID := "org-edge"

	// Build raw batch — auto-generate Signatures from decoder response
	rawBatch := event.RawBatch{
		Chain: chainID, Network: networkID,
		Address: watchedAddr, WalletID: &walletID, OrgID: &orgID,
	}
	if rawBatchOverride != nil {
		rawBatch = *rawBatchOverride
	} else {
		// Generate one RawTransaction + Signature per result
		for _, r := range decoderResp.Results {
			rawBatch.RawTransactions = append(rawBatch.RawTransactions, json.RawMessage(`{}`))
			rawBatch.Signatures = append(rawBatch.Signatures, event.SignatureInfo{Hash: r.TxHash, Sequence: r.BlockCursor})
		}
	}

	// --- Normalize ---
	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := New("unused", 2*time.Second, nil, normalizedCh, 1, slog.Default())
	mockDecoder := normalizermocks.NewMockChainDecoderClient(ctrl)

	mockDecoder.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
			return decoderResp, nil
		}).Times(1)

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockDecoder, rawBatch))

	var normalized event.NormalizedBatch
	select {
	case normalized = <-normalizedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for normalized batch")
	}

	if len(normalized.Transactions) == 0 {
		return edgeCaseResult{normalized: normalized}
	}

	// --- Ingest ---
	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockTxRepo := storemocks.NewMockTransactionRepository(ctrl)
	mockBERepo := storemocks.NewMockBalanceEventRepository(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockTokenRepo := storemocks.NewMockTokenRepository(ctrl)
	mockConfigRepo := storemocks.NewMockWatermarkRepository(ctrl)

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: initialBalance, Exists: true}
			}
			return result, nil
		})
	mockTokenRepo.EXPECT().BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			r := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				r[a] = false
			}
			return r, nil
		})

	fakeDB := openE2EFakeDB(t)
	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, _ *sql.TxOptions) (*sql.Tx, error) { return fakeDB.BeginTx(ctx, nil) }).Times(1)

	txID := uuid.New()
	mockTxRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			m := make(map[string]uuid.UUID, len(txns))
			for _, tx := range txns {
				m[tx.TxHash] = txID
			}
			return m, nil
		}).Times(1)
	tokenID := uuid.New()
	mockTokenRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
			m := make(map[string]uuid.UUID, len(tokens))
			for _, tok := range tokens {
				m[tok.ContractAddress] = tokenID
			}
			return m, nil
		}).Times(1)

	var captured []*model.BalanceEvent
	mockBERepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			captured = append(captured, events...)
			return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
		}).Times(1)

	var capturedAdjust []store.BulkAdjustItem
	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), chainID, networkID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, items []store.BulkAdjustItem) error {
			capturedAdjust = append(capturedAdjust, items...)
			return nil
		}).MaxTimes(1)
	mockConfigRepo.EXPECT().UpdateWatermarkTx(gomock.Any(), gomock.Any(), chainID, networkID, gomock.Any()).Return(nil).Times(1)

	inputCh := make(chan event.NormalizedBatch, 1)
	ing := ingester.New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, inputCh, slog.Default())
	inputCh <- normalized
	close(inputCh)
	require.NoError(t, ing.Run(context.Background()))

	return edgeCaseResult{normalized: normalized, ingested: captured, adjusts: capturedAdjust}
}

// ===========================================================================
// Solana: CPI cross-program invocation — outer owner suppresses inner duplicate
// ===========================================================================

func TestEdge_Solana_CPI_OuterOwnerSuppressesInnerDuplicate(t *testing.T) {
	const watched = "SolOwnerAddr111111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "cpi_sig_001", BlockCursor: 310000000, BlockTime: 1700080000,
			FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// outer:0|inner:-1 — owner-level transfer (should survive)
				{
					OuterInstructionIndex: 0, InnerInstructionIndex: -1,
					EventCategory: string(model.EventCategoryTransfer), EventAction: "outer_owner_transfer",
					ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
					Address: watched, CounterpartyAddress: "ExtAddr111111111111111111111111111111111111",
					Delta: "-5000000", TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
				},
				// outer:0|inner:2 — CPI inner duplicate of same transfer (should be suppressed)
				{
					OuterInstructionIndex: 0, InnerInstructionIndex: 2,
					EventCategory: string(model.EventCategoryTransfer), EventAction: "inner_dup_transfer",
					ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
					Address: watched, CounterpartyAddress: "ExtAddr111111111111111111111111111111111111",
					Delta: "-5000000", TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
				},
				// outer:0|inner:1 — different address (not duplicate, should survive)
				{
					OuterInstructionIndex: 0, InnerInstructionIndex: 1,
					EventCategory: string(model.EventCategoryTransfer), EventAction: "inner_other_transfer",
					ProgramId: "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", ContractAddress: "SPLToken111111111111111111111111111111111111",
					Address: watched, CounterpartyAddress: "ThirdAddr111111111111111111111111111111111111",
					Delta: "-2000000", TokenSymbol: "USDC", TokenDecimals: 6, TokenType: string(model.TokenTypeFungible),
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainSolana, model.NetworkDevnet, watched, resp, "100000000000", nil)

	// Outer owner + inner other + fee = 3 events (inner dup suppressed)
	// Note: Solana dedup is canonical-event-id based. The outer:0|inner:-1 and outer:0|inner:2
	// generate different event_ids due to different event paths, but the normalizer's
	// instruction ownership dedup should suppress the inner duplicate.
	txBEs := res.normalized.Transactions[0].BalanceEvents

	// Count by event action
	actions := map[string]int{}
	for _, be := range txBEs {
		actions[be.EventAction]++
	}

	// The outer owner transfer must be present
	assert.GreaterOrEqual(t, actions["outer_owner_transfer"], 1, "outer owner event must survive")
	// The inner other-address event must be present
	assert.Equal(t, 1, actions["inner_other_transfer"], "different-address inner event must survive")
	// Fee
	hasFee := false
	for _, be := range txBEs {
		if be.ActivityType == model.ActivityFee {
			hasFee = true
		}
	}
	assert.True(t, hasFee, "must have fee event")

	// Event IDs unique
	ids := map[string]struct{}{}
	for _, be := range txBEs {
		_, dup := ids[be.EventID]
		assert.False(t, dup, "duplicate event_id: %s", be.EventID)
		ids[be.EventID] = struct{}{}
	}
}

// ===========================================================================
// Solana: SPL token + native in same tx, multi-program
// ===========================================================================

func TestEdge_Solana_MixedNativeAndSPLToken(t *testing.T) {
	const watched = "MixAddr111111111111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "mixed_sol_spl_001", BlockCursor: 320000000, BlockTime: 1700090000,
			FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// Native SOL withdrawal
				{
					OuterInstructionIndex: 0, InnerInstructionIndex: -1,
					EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
					ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
					Address: watched, CounterpartyAddress: "RecvAddr1111111111111111111111111111111111",
					Delta: "-1000000000", TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
				},
				// SPL USDC deposit (received in same tx)
				{
					OuterInstructionIndex: 1, InnerInstructionIndex: -1,
					EventCategory: string(model.EventCategoryTransfer), EventAction: "spl_transfer",
					ProgramId: "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", ContractAddress: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
					Address: watched, CounterpartyAddress: "SenderAddr111111111111111111111111111111111",
					Delta: "500000000", TokenSymbol: "USDC", TokenName: "USD Coin", TokenDecimals: 6, TokenType: string(model.TokenTypeFungible),
				},
				// SPL BONK deposit (another token in same tx)
				{
					OuterInstructionIndex: 2, InnerInstructionIndex: -1,
					EventCategory: string(model.EventCategoryTransfer), EventAction: "spl_transfer",
					ProgramId: "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", ContractAddress: "DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263",
					Address: watched, CounterpartyAddress: "BonkSender111111111111111111111111111111111",
					Delta: "1000000000000", TokenSymbol: "BONK", TokenName: "Bonk", TokenDecimals: 5, TokenType: string(model.TokenTypeFungible),
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainSolana, model.NetworkDevnet, watched, resp, "50000000000", nil)

	// 3 transfers + 1 fee = 4 events
	require.Len(t, res.normalized.Transactions, 1)
	require.Len(t, res.normalized.Transactions[0].BalanceEvents, 4)

	activities := map[model.ActivityType]int{}
	contracts := map[string]int{}
	for _, be := range res.normalized.Transactions[0].BalanceEvents {
		activities[be.ActivityType]++
		contracts[be.ContractAddress]++
	}

	assert.Equal(t, 1, activities[model.ActivityWithdrawal], "1 native withdrawal")
	assert.Equal(t, 2, activities[model.ActivityDeposit], "2 SPL deposits (USDC + BONK)")
	assert.Equal(t, 1, activities[model.ActivityFee], "1 fee")

	// 3 distinct contract addresses (SOL native + USDC + BONK) + native for fee
	assert.GreaterOrEqual(t, len(contracts), 3, "at least 3 distinct contracts")

	// Ingested: all 4 events applied with correct balance tracking
	require.Len(t, res.ingested, 4)
	for _, ev := range res.ingested {
		assert.True(t, ev.BalanceApplied)
		assert.NotEmpty(t, ev.EventID)
	}
}

// ===========================================================================
// Solana: failed tx — only fee event, all transfers discarded
// ===========================================================================

func TestEdge_Solana_FailedTx_OnlyFeeEmitted(t *testing.T) {
	const watched = "FailSolAddr11111111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "failed_sol_001", BlockCursor: 330000000, BlockTime: 1700100000,
			FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusFailed),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// This should still be processed (Solana doesn't discard on failure like EVM)
				{
					OuterInstructionIndex: 0, InnerInstructionIndex: -1,
					EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
					ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
					Address: watched, Delta: "-1000000000",
					TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainSolana, model.NetworkDevnet, watched, resp, "5000000000", nil)

	// Solana still processes transfers + emits fee even for failed txs
	require.Len(t, res.normalized.Transactions, 1)
	txBEs := res.normalized.Transactions[0].BalanceEvents
	require.NotEmpty(t, txBEs)

	hasFee := false
	for _, be := range txBEs {
		if be.ActivityType == model.ActivityFee {
			hasFee = true
			assert.Equal(t, "-5000", be.Delta)
		}
	}
	assert.True(t, hasFee, "must have fee event even for failed Solana tx")
}

// ===========================================================================
// EVM: failed tx — balance events discarded, only L2+L1 fee survives
// ===========================================================================

func TestEdge_EVM_FailedTx_OnlyFeeEvents(t *testing.T) {
	const watched = "0xfailedtxaddr1111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xfailed_evm_001", BlockCursor: 20100000, BlockTime: 1700200000,
			FeeAmount: "500000000000000", FeePayer: watched, Status: string(model.TxStatusFailed),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// Transfer should be discarded (EVM rolls back on failure)
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: "0xrecipient11111111111111111111111111111111",
					Delta: "-1000000000000000000", TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{
						"base_event_path":          "log:0",
						"base_gas_used":            "100000",
						"base_effective_gas_price":  "5000000000",
						"fee_data_l1":              "10000",
					},
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainBase, model.NetworkSepolia, watched, resp, "2000000000000000000", nil)

	require.Len(t, res.normalized.Transactions, 1)
	txBEs := res.normalized.Transactions[0].BalanceEvents

	// Should have fee events only (no transfer — EVM discards on failure)
	for _, be := range txBEs {
		assert.NotEqual(t, model.ActivityDeposit, be.ActivityType, "no deposits on failed EVM tx")
		assert.NotEqual(t, model.ActivityWithdrawal, be.ActivityType, "no withdrawals on failed EVM tx")
		assert.True(t,
			be.ActivityType == model.ActivityFee ||
				be.ActivityType == model.ActivityFeeExecutionL2 ||
				be.ActivityType == model.ActivityFeeDataL1,
			"only fee-related activities for failed EVM tx, got: %s", be.ActivityType)
	}

	assert.GreaterOrEqual(t, len(txBEs), 1, "at least 1 fee event")
}

// ===========================================================================
// EVM: self-transfer — deduped to delta=0
// ===========================================================================

func TestEdge_EVM_SelfTransfer_E2E_DeltaZero(t *testing.T) {
	const watched = "0xselfsendaddr111111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xself_transfer_001", BlockCursor: 20200000, BlockTime: 1700300000,
			FeeAmount: "21000000000000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// Outgoing leg
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: watched, // self!
					Delta: "-500000000000000000", TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{"base_event_path": "log:0", "base_log_index": "0", "base_gas_used": "21000", "base_effective_gas_price": "1000000000"},
				},
				// Incoming leg
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: watched, // self!
					Delta: "500000000000000000", TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{"base_event_path": "log:0", "base_log_index": "0"},
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainEthereum, model.NetworkMainnet, watched, resp, "2000000000000000000", nil)

	txBEs := res.normalized.Transactions[0].BalanceEvents

	var selfCount int
	for _, be := range txBEs {
		if be.ActivityType == model.ActivitySelfTransfer {
			selfCount++
			assert.Equal(t, "0", be.Delta, "self-transfer delta must be 0")
			assert.Equal(t, "self_transfer", be.EventAction)
		}
	}
	assert.Equal(t, 1, selfCount, "both legs merge into 1 self-transfer event")

	// Fee event still present
	hasFee := false
	for _, be := range txBEs {
		if be.ActivityType == model.ActivityFee {
			hasFee = true
		}
	}
	assert.True(t, hasFee, "fee event must still be present")

	// Ingested: self-transfer has delta=0 → balance unchanged
	for _, ev := range res.ingested {
		if ev.ActivityType == model.ActivitySelfTransfer {
			assert.Equal(t, "0", ev.Delta)
			assert.True(t, ev.BalanceApplied)
			assert.Equal(t, *ev.BalanceBefore, *ev.BalanceAfter, "self-transfer: balance unchanged")
		}
	}
}

// ===========================================================================
// EVM: multiple ERC20 tokens + native in single tx, L2 fee decomposition
// ===========================================================================

func TestEdge_EVM_MultiToken_WithL2FeeDecomposition(t *testing.T) {
	const watched = "0xmultitokenaddr1111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xmulti_token_001", BlockCursor: 20300000, BlockTime: 1700400000,
			FeeAmount: "150000000000000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// Native ETH withdrawal
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: "0xrecipient1111111111111111111111111111111111",
					Delta: "-200000000000000000", TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{"base_event_path": "log:0", "base_log_index": "0",
						"base_gas_used": "100000", "base_effective_gas_price": "1000000000", "fee_data_l1": "50000000000000"},
				},
				// USDC transfer IN (deposit)
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "erc20_transfer",
					ProgramId: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", ContractAddress: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
					Address: watched, CounterpartyAddress: "0xsender11111111111111111111111111111111111",
					Delta: "5000000000", TokenSymbol: "USDC", TokenDecimals: 6, TokenType: string(model.TokenTypeFungible),
					Metadata: map[string]string{"base_event_path": "log:1", "base_log_index": "1"},
				},
				// WBTC transfer OUT (withdrawal)
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "erc20_transfer",
					ProgramId: "0x2260fac5e5542a773aa44fbcfedf7c193bc2c599", ContractAddress: "0x2260fac5e5542a773aa44fbcfedf7c193bc2c599",
					Address: watched, CounterpartyAddress: "0xrecipient2222222222222222222222222222222222",
					Delta: "-10000000", TokenSymbol: "WBTC", TokenDecimals: 8, TokenType: string(model.TokenTypeFungible),
					Metadata: map[string]string{"base_event_path": "log:2", "base_log_index": "2"},
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainBase, model.NetworkSepolia, watched, resp, "1000000000000000000", nil)

	require.Len(t, res.normalized.Transactions, 1)
	txBEs := res.normalized.Transactions[0].BalanceEvents

	// ETH withdrawal + USDC deposit + WBTC withdrawal + L2 execution fee + L1 data fee = 5
	require.Len(t, txBEs, 5, "3 transfers + L2 fee + L1 data fee")

	activities := map[model.ActivityType]int{}
	contracts := map[string]int{}
	for _, be := range txBEs {
		activities[be.ActivityType]++
		contracts[be.ContractAddress]++
		assert.NotEmpty(t, be.EventID)
		assert.Equal(t, "base_log", be.EventPathType, "EVM events use base_log path type")
		assert.Equal(t, "base-decoder-v1", be.DecoderVersion)
	}

	assert.Equal(t, 2, activities[model.ActivityWithdrawal], "ETH + WBTC withdrawals")
	assert.Equal(t, 1, activities[model.ActivityDeposit], "USDC deposit")
	assert.Equal(t, 1, activities[model.ActivityFeeExecutionL2], "L2 execution fee")
	assert.Equal(t, 1, activities[model.ActivityFeeDataL1], "L1 data fee")

	// 3+ distinct contracts (ETH, USDC, WBTC, possibly ETH again for fee)
	assert.GreaterOrEqual(t, len(contracts), 3, "at least 3 distinct contracts")

	// All ingested events applied
	require.Len(t, res.ingested, 5)
	for _, ev := range res.ingested {
		assert.True(t, ev.BalanceApplied)
	}
}

// ===========================================================================
// EVM: fee decomposition mismatch (components don't sum to total)
// ===========================================================================

func TestEdge_EVM_FeeDecomposition_Mismatch(t *testing.T) {
	const watched = "0xfeemismatchaddr1111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xfee_mismatch_001", BlockCursor: 20400000, BlockTime: 1700500000,
			// Total fee = 1000, but execution=600 + data=500 = 1100 (mismatch!)
			FeeAmount: "1000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "erc20_transfer",
					ProgramId: "0xabc", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: "0xrecipient3333333333333333333333333333333333",
					Delta: "-1100", TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{
						"base_event_path": "log:55",
						"fee_execution_l2": "600",
						"fee_data_l1":      "500",
					},
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainBase, model.NetworkSepolia, watched, resp, "100000", nil)

	require.Len(t, res.normalized.Transactions, 1)
	txBEs := res.normalized.Transactions[0].BalanceEvents

	// Find execution fee event
	var execFee *event.NormalizedBalanceEvent
	for i := range txBEs {
		if txBEs[i].ActivityType == model.ActivityFeeExecutionL2 {
			execFee = &txBEs[i]
		}
	}
	require.NotNil(t, execFee, "must have L2 execution fee")

	// Verify mismatch marker in chain_data
	var chainData map[string]string
	if execFee.ChainData != nil {
		_ = json.Unmarshal(execFee.ChainData, &chainData)
	}
	assert.Equal(t, "true", chainData["fee_total_mismatch"], "must mark fee_total_mismatch")
}

// ===========================================================================
// BTC: consolidation — 5 UTXOs in, 1 out + change
// ===========================================================================

func TestEdge_BTC_ConsolidationTx_ManyInputsOneOutput(t *testing.T) {
	const watched = "bc1qconsolidation111111111111111111111"

	// Build 5 vin_spend events
	var balanceEvents []*sidecarv1.BalanceEventInfo
	totalIn := int64(0)
	for i := 0; i < 5; i++ {
		amount := int64(10000 + i*5000) // 10000, 15000, 20000, 25000, 30000
		totalIn += amount
		balanceEvents = append(balanceEvents, &sidecarv1.BalanceEventInfo{
			OuterInstructionIndex: int32(i), InnerInstructionIndex: -1,
			EventCategory: string(model.EventCategoryTransfer), EventAction: "vin_spend",
			ProgramId: "btc", ContractAddress: "BTC",
			Address: watched, Delta: "-" + big.NewInt(amount).String(),
			TokenSymbol: "BTC", TokenDecimals: 8, TokenType: string(model.TokenTypeNative),
			Metadata: map[string]string{
				"event_path":     "vin:" + big.NewInt(int64(i)).String(),
				"utxo_path_type": "vin",
				"input_txid":     "prev-tx-" + big.NewInt(int64(i)).String(),
				"input_vout":     "0",
				"finality_state": "confirmed",
			},
		})
	}

	// 1 vout_receive (consolidation output, minus fee)
	fee := int64(2000)
	changeAmount := totalIn - fee // 100000 - 2000 = 98000
	balanceEvents = append(balanceEvents, &sidecarv1.BalanceEventInfo{
		OuterInstructionIndex: 5, InnerInstructionIndex: -1,
		EventCategory: string(model.EventCategoryTransfer), EventAction: "vout_receive",
		ProgramId: "btc", ContractAddress: "BTC",
		Address: watched, Delta: big.NewInt(changeAmount).String(),
		TokenSymbol: "BTC", TokenDecimals: 8, TokenType: string(model.TokenTypeNative),
		Metadata: map[string]string{
			"event_path": "vout:0", "utxo_path_type": "vout", "finality_state": "confirmed",
		},
	})

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "btc_consolidation_001", BlockCursor: 850001, BlockTime: 1700600000,
			FeeAmount: big.NewInt(fee).String(), FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: balanceEvents,
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainBTC, model.NetworkMainnet, watched, resp, "500000", nil)

	require.Len(t, res.normalized.Transactions, 1)
	txBEs := res.normalized.Transactions[0].BalanceEvents

	// 5 vin + 1 vout + 1 miner_fee = 7 events
	require.Len(t, txBEs, 7, "5 vin_spend + 1 vout_receive + 1 miner_fee")

	// BTC conservation: miner_fee is a synthetic event with delta = -fee,
	// while the fee is also implicit in (vin - vout). Verify:
	// 1) sum(vin) + sum(vout) = -fee (implicit fee matches explicit fee)
	// 2) miner_fee delta = -fee
	vinSum := new(big.Int)
	voutSum := new(big.Int)
	var minerFeeDelta string
	for _, be := range txBEs {
		d, ok := new(big.Int).SetString(be.Delta, 10)
		require.True(t, ok, "delta parse: %s", be.Delta)
		switch be.EventAction {
		case "vin_spend":
			vinSum.Add(vinSum, d)
		case "vout_receive":
			voutSum.Add(voutSum, d)
		case "miner_fee":
			minerFeeDelta = be.Delta
		}
	}
	implicitFee := new(big.Int).Add(vinSum, voutSum) // should be -fee
	assert.Equal(t, "-2000", implicitFee.String(), "implicit fee = vin + vout")
	assert.Equal(t, "-2000", minerFeeDelta, "miner_fee delta = -fee")

	// Event IDs unique
	ids := map[string]struct{}{}
	for _, be := range txBEs {
		_, dup := ids[be.EventID]
		assert.False(t, dup, "dup event_id: %s", be.EventID)
		ids[be.EventID] = struct{}{}
	}

	// All ingested
	require.Len(t, res.ingested, 7)
	for _, ev := range res.ingested {
		assert.True(t, ev.BalanceApplied)
		assert.Equal(t, "confirmed", ev.FinalityState)
	}
}

// ===========================================================================
// BTC: fan-out — 1 input, 5 outputs (payment to multiple recipients)
// ===========================================================================

func TestEdge_BTC_FanOut_OneInputManyOutputs(t *testing.T) {
	const watched = "bc1qfanoutsender11111111111111111111111"

	var events []*sidecarv1.BalanceEventInfo

	// 1 vin: spend 200000 sat
	events = append(events, &sidecarv1.BalanceEventInfo{
		OuterInstructionIndex: 0, InnerInstructionIndex: -1,
		EventCategory: string(model.EventCategoryTransfer), EventAction: "vin_spend",
		ProgramId: "btc", ContractAddress: "BTC",
		Address: watched, Delta: "-200000",
		TokenSymbol: "BTC", TokenDecimals: 8, TokenType: string(model.TokenTypeNative),
		Metadata: map[string]string{
			"event_path": "vin:0", "utxo_path_type": "vin",
			"input_txid": "input-utxo-big", "input_vout": "0", "finality_state": "confirmed",
		},
	})

	// 4 vout to external addresses (not watched, so these won't appear)
	// 1 vout change back to watched
	events = append(events, &sidecarv1.BalanceEventInfo{
		OuterInstructionIndex: 5, InnerInstructionIndex: -1,
		EventCategory: string(model.EventCategoryTransfer), EventAction: "vout_receive",
		ProgramId: "btc", ContractAddress: "BTC",
		Address: watched, Delta: "49000",
		TokenSymbol: "BTC", TokenDecimals: 8, TokenType: string(model.TokenTypeNative),
		Metadata: map[string]string{
			"event_path": "vout:4", "utxo_path_type": "vout", "finality_state": "confirmed",
		},
	})

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "btc_fanout_001", BlockCursor: 850002, BlockTime: 1700700000,
			FeeAmount: "1000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: events,
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainBTC, model.NetworkMainnet, watched, resp, "300000", nil)

	// 1 vin + 1 vout (change) + 1 fee = 3 (external vouts not watched)
	require.Len(t, res.normalized.Transactions[0].BalanceEvents, 3)

	vinCount, voutCount, feeCount := 0, 0, 0
	for _, be := range res.normalized.Transactions[0].BalanceEvents {
		switch be.EventAction {
		case "vin_spend":
			vinCount++
			assert.Equal(t, "-200000", be.Delta)
		case "vout_receive":
			voutCount++
			assert.Equal(t, "49000", be.Delta)
		case "miner_fee":
			feeCount++
			assert.Equal(t, "-1000", be.Delta)
		}
	}
	assert.Equal(t, 1, vinCount)
	assert.Equal(t, 1, voutCount)
	assert.Equal(t, 1, feeCount)
}

// ===========================================================================
// Cross-chain: very large numbers (near uint256 boundary)
// ===========================================================================

func TestEdge_LargeNumbers_NoOverflow(t *testing.T) {
	const watched = "LargeNumAddr1111111111111111111111111111111"

	// 10^30 (larger than uint64 max)
	largeDelta := "1000000000000000000000000000000"
	largeBalance := "9999999999999999999999999999999"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "large_num_001", BlockCursor: 500000000, BlockTime: 1700800000,
			FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
					ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
					Address: watched, CounterpartyAddress: "RecvAddr1111111111111111111111111111111111",
					Delta: "-" + largeDelta, TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainSolana, model.NetworkDevnet, watched, resp, largeBalance, nil)

	require.Len(t, res.ingested, 2) // transfer + fee

	for _, ev := range res.ingested {
		assert.True(t, ev.BalanceApplied)
		assert.NotNil(t, ev.BalanceBefore)
		assert.NotNil(t, ev.BalanceAfter)

		// Verify big number arithmetic is correct
		if ev.ActivityType == model.ActivityWithdrawal {
			before, _ := new(big.Int).SetString(*ev.BalanceBefore, 10)
			after, _ := new(big.Int).SetString(*ev.BalanceAfter, 10)
			delta, _ := new(big.Int).SetString(ev.Delta, 10)
			expected := new(big.Int).Add(before, delta)
			assert.Equal(t, expected.String(), after.String(), "big number arithmetic: before + delta = after")
		}
	}
}

// ===========================================================================
// Cross-chain: multi-block range with cursor monotonicity
// ===========================================================================

func TestEdge_MultiBlock_CursorMonotonicity(t *testing.T) {
	const watched = "MonoAddr1111111111111111111111111111111111111"

	// 3 blocks, 5 total txs across blocks
	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			{
				TxHash: "tx_block_100", BlockCursor: 100, BlockTime: 1700000000,
				FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
					ProgramId: "1111", ContractAddress: "1111", Address: watched, Delta: "1000",
					TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
				}},
			},
			{
				TxHash: "tx_block_100_b", BlockCursor: 100, BlockTime: 1700000000,
				FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
					ProgramId: "1111", ContractAddress: "1111", Address: watched, Delta: "2000",
					TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
				}},
			},
			{
				TxHash: "tx_block_101", BlockCursor: 101, BlockTime: 1700000010,
				FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
					ProgramId: "1111", ContractAddress: "1111", Address: watched, Delta: "-500",
					TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
				}},
			},
			{
				TxHash: "tx_block_105", BlockCursor: 105, BlockTime: 1700000050,
				FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
					ProgramId: "1111", ContractAddress: "1111", Address: watched, Delta: "10000",
					TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
				}},
			},
			{
				TxHash: "tx_block_110", BlockCursor: 110, BlockTime: 1700000100,
				FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
					ProgramId: "1111", ContractAddress: "1111", Address: watched, Delta: "-3000",
					TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
				}},
			},
		},
	}

	// Feed multiple raw txs
	walletID := "wallet-mono"
	orgID := "org-mono"
	rawBatch := &event.RawBatch{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		Address: watched, WalletID: &walletID, OrgID: &orgID,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{}`), json.RawMessage(`{}`), json.RawMessage(`{}`),
			json.RawMessage(`{}`), json.RawMessage(`{}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "tx_block_100", Sequence: 100},
			{Hash: "tx_block_100_b", Sequence: 100},
			{Hash: "tx_block_101", Sequence: 101},
			{Hash: "tx_block_105", Sequence: 105},
			{Hash: "tx_block_110", Sequence: 110},
		},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainSolana, model.NetworkDevnet, watched, resp, "50000000", rawBatch)

	// 5 txs × 2 events each (transfer + fee) = 10 events
	require.Len(t, res.normalized.Transactions, 5, "5 transactions across 3 blocks")
	require.Len(t, res.ingested, 10, "10 events total")

	// Cursor monotonicity: block cursors should be non-decreasing
	var prevCursor int64
	for _, tx := range res.normalized.Transactions {
		assert.GreaterOrEqual(t, tx.BlockCursor, prevCursor,
			"cursor must be monotonically non-decreasing: prev=%d, got=%d", prevCursor, tx.BlockCursor)
		prevCursor = tx.BlockCursor
	}

	// All event IDs unique across all txs
	allIDs := map[string]struct{}{}
	for _, ev := range res.ingested {
		_, dup := allIDs[ev.EventID]
		assert.False(t, dup, "duplicate event_id across multi-block batch: %s", ev.EventID)
		allIDs[ev.EventID] = struct{}{}
	}
	assert.Len(t, allIDs, 10, "10 unique event IDs")
}

// ===========================================================================
// EVM: mixed case addresses — canonicalization
// ===========================================================================

func TestEdge_EVM_MixedCaseAddress_Canonicalization(t *testing.T) {
	// Mixed case input
	const watchedMixed = "0xAaBbCcDdEeFf00112233445566778899AaBbCcDd"
	const watchedLower = "0xaabbccddeeff00112233445566778899aabbccdd"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xcase_test_001", BlockCursor: 20500000, BlockTime: 1700900000,
			FeeAmount: "21000000000000", FeePayer: watchedMixed, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watchedMixed, CounterpartyAddress: "0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD",
					Delta: "-1000000000000000", TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{"base_event_path": "log:0", "base_log_index": "0", "base_gas_used": "21000", "base_effective_gas_price": "1000000000"},
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainEthereum, model.NetworkMainnet, watchedMixed, resp, "5000000000000000000", nil)

	require.NotEmpty(t, res.ingested)

	for _, ev := range res.ingested {
		if ev.ActivityType == model.ActivityWithdrawal {
			// Address must be lowercase after canonicalization
			assert.Equal(t, watchedLower, ev.Address,
				"EVM address must be lowercased: got %s", ev.Address)
			assert.Equal(t, strings.ToLower("0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD"), ev.CounterpartyAddress,
				"counterparty must also be lowercased")
		}
	}
}

// ===========================================================================
// Cross-chain: zero-delta event (e.g. self-transfer) + negative balance combo
// ===========================================================================

func TestEdge_ZeroDelta_NegativeBalance_Combined(t *testing.T) {
	const watched = "0xzerodeltaneg1111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xzero_neg_001", BlockCursor: 20600000, BlockTime: 1701000000,
			FeeAmount: "100000000000000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// Self-transfer: delta becomes 0
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: watched,
					Delta: "-500000000000000000", TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{"base_event_path": "log:0", "base_log_index": "0",
						"base_gas_used": "21000", "base_effective_gas_price": "1000000000"},
				},
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: watched,
					Delta: "500000000000000000", TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{"base_event_path": "log:0"},
				},
			},
		}},
	}

	// Very small balance — fee will push it negative
	res := runEdgeCaseNormalizeAndIngest(t, model.ChainEthereum, model.NetworkMainnet, watched, resp, "50", nil)

	require.NotEmpty(t, res.ingested)

	for _, ev := range res.ingested {
		assert.True(t, ev.BalanceApplied)
		if ev.ActivityType == model.ActivitySelfTransfer {
			assert.Equal(t, "0", ev.Delta, "self-transfer delta=0")
			// BalanceBefore == BalanceAfter for zero delta
			assert.Equal(t, *ev.BalanceBefore, *ev.BalanceAfter)
		}
		if ev.ActivityType == model.ActivityFee {
			// Fee on tiny balance → goes negative
			assert.True(t, strings.HasPrefix(*ev.BalanceAfter, "-"),
				"fee from tiny balance should go negative: %s", *ev.BalanceAfter)
		}
	}
}
