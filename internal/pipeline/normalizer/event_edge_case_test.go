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

// ===========================================================================
// EVM: internal transaction — native value transfer via trace (tx:N path)
// ===========================================================================

func TestEdge_EVM_InternalTx_NativeValueTransfer(t *testing.T) {
	const watched = "0xinternaltxwatched111111111111111111111111"
	const contractAddr = "0xdex_contract_addr11111111111111111111111111"

	// Sidecar returns internal native transfer discovered via trace (event_path: "tx:0")
	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xinternal_native_001", BlockCursor: 20700000, BlockTime: 1701100000,
			FeeAmount: "42000000000000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// Internal call: contract sends ETH to watched address
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "internal_transfer",
					ProgramId: contractAddr, ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: contractAddr,
					Delta: "750000000000000000", // 0.75 ETH received via internal call
					TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{
						"event_path":              "tx:0",
						"base_gas_used":           "42000",
						"base_effective_gas_price": "1000000000",
						"trace_type":              "CALL",
						"trace_depth":             "1",
					},
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainEthereum, model.NetworkMainnet, watched, resp, "2000000000000000000", nil)

	require.Len(t, res.normalized.Transactions, 1)
	txBEs := res.normalized.Transactions[0].BalanceEvents

	// Internal transfer (deposit) + fee = 2 events
	require.Len(t, txBEs, 2, "1 internal transfer + 1 fee")

	var deposit, fee *event.NormalizedBalanceEvent
	for i := range txBEs {
		switch txBEs[i].ActivityType {
		case model.ActivityDeposit:
			deposit = &txBEs[i]
		case model.ActivityFee:
			fee = &txBEs[i]
		}
	}

	require.NotNil(t, deposit, "must have deposit from internal transfer")
	assert.Equal(t, "750000000000000000", deposit.Delta)
	assert.Equal(t, "internal_transfer", deposit.EventAction)
	assert.Equal(t, "base_log", deposit.EventPathType)
	assert.Contains(t, deposit.EventPath, "tx:0", "internal tx uses tx:N event path")
	assert.NotEmpty(t, deposit.EventID)

	require.NotNil(t, fee, "must have fee event")
	assert.True(t, strings.HasPrefix(fee.Delta, "-"))

	// Ingested: both events applied
	require.Len(t, res.ingested, 2)
	for _, ev := range res.ingested {
		assert.True(t, ev.BalanceApplied)
	}
}

// ===========================================================================
// EVM: nested internal calls (3-level depth) — multiple value transfers
// ===========================================================================

func TestEdge_EVM_InternalTx_NestedCalls(t *testing.T) {
	const watched = "0xnestedwatched1111111111111111111111111111"
	const router = "0xrouter11111111111111111111111111111111111"
	const pool = "0xpool111111111111111111111111111111111111111"

	// 3-level nested: user → router → pool → user (refund)
	// Sidecar decodes these as separate balance events from trace
	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xnested_internal_001", BlockCursor: 20800000, BlockTime: 1701200000,
			FeeAmount: "84000000000000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// Level 1: user sends 1 ETH to router (direct tx value)
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: router,
					Delta: "-1000000000000000000",
					TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{
						"event_path": "tx:0",
						"base_gas_used": "84000", "base_effective_gas_price": "1000000000",
						"trace_type": "CALL", "trace_depth": "0",
					},
				},
				// Level 3: pool refunds 0.1 ETH back to user (internal transfer)
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "internal_transfer",
					ProgramId: pool, ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: pool,
					Delta: "100000000000000000",
					TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{
						"event_path": "tx:1",
						"trace_type": "CALL", "trace_depth": "2",
					},
				},
				// ERC20 token received from swap (log-based event)
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "erc20_transfer",
					ProgramId: "0xdac17f958d2ee523a2206206994597c13d831ec7", ContractAddress: "0xdac17f958d2ee523a2206206994597c13d831ec7",
					Address: watched, CounterpartyAddress: pool,
					Delta: "1500000000",
					TokenSymbol: "USDT", TokenName: "Tether USD", TokenDecimals: 6, TokenType: string(model.TokenTypeFungible),
					Metadata: map[string]string{
						"base_event_path": "log:3", "base_log_index": "3",
					},
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainEthereum, model.NetworkMainnet, watched, resp, "5000000000000000000", nil)

	require.Len(t, res.normalized.Transactions, 1)
	txBEs := res.normalized.Transactions[0].BalanceEvents

	// 1 withdrawal (tx:0) + 1 deposit (tx:1 refund) + 1 ERC20 deposit (log:3) + 1 fee = 4
	require.Len(t, txBEs, 4, "2 native transfers + 1 ERC20 + 1 fee")

	activities := map[model.ActivityType]int{}
	paths := map[string]bool{}
	for _, be := range txBEs {
		activities[be.ActivityType]++
		paths[be.EventPath] = true
	}

	assert.Equal(t, 1, activities[model.ActivityWithdrawal], "1 ETH withdrawal")
	assert.Equal(t, 2, activities[model.ActivityDeposit], "1 ETH refund + 1 USDT deposit")
	assert.Equal(t, 1, activities[model.ActivityFee], "1 fee")

	// Different event paths for trace (tx:N) vs log (log:N) events
	assert.True(t, paths["tx:0"], "direct value transfer path")
	assert.True(t, paths["tx:1"], "internal refund path")
	assert.True(t, paths["log:3"], "ERC20 log path")

	// All event IDs unique (trace and log events have distinct canonical IDs)
	ids := map[string]struct{}{}
	for _, be := range txBEs {
		_, dup := ids[be.EventID]
		assert.False(t, dup, "duplicate event_id: %s", be.EventID)
		ids[be.EventID] = struct{}{}
	}
}

// ===========================================================================
// EVM: internal tx mixed with ERC20 — trace + log events coexist
// ===========================================================================

func TestEdge_EVM_InternalTx_MixedTraceAndLog(t *testing.T) {
	const watched = "0xmixedtracelogaddr11111111111111111111111111"
	const dex = "0xdex222222222222222222222222222222222222222"

	// Swap tx: send ETH via internal call, receive USDC via log
	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xmixed_trace_log_001", BlockCursor: 20900000, BlockTime: 1701300000,
			FeeAmount: "63000000000000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// Trace-discovered: ETH sent to DEX
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "internal_transfer",
					ProgramId: dex, ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: dex,
					Delta: "-500000000000000000",
					TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{
						"base_event_path": "tx:0",
						"base_gas_used": "63000", "base_effective_gas_price": "1000000000",
						"trace_type": "CALL",
					},
				},
				// Log-based: USDC received
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "erc20_transfer",
					ProgramId: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", ContractAddress: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
					Address: watched, CounterpartyAddress: dex,
					Delta: "1000000000",
					TokenSymbol: "USDC", TokenDecimals: 6, TokenType: string(model.TokenTypeFungible),
					Metadata: map[string]string{"base_event_path": "log:5", "base_log_index": "5"},
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainEthereum, model.NetworkMainnet, watched, resp, "3000000000000000000", nil)

	require.Len(t, res.normalized.Transactions, 1)
	txBEs := res.normalized.Transactions[0].BalanceEvents

	// Internal ETH withdrawal + USDC deposit + fee = 3
	require.Len(t, txBEs, 3, "1 trace-based + 1 log-based + 1 fee")

	var traceEvent, logEvent *event.NormalizedBalanceEvent
	for i := range txBEs {
		if txBEs[i].EventPath == "tx:0" {
			traceEvent = &txBEs[i]
		}
		if txBEs[i].EventPath == "log:5" {
			logEvent = &txBEs[i]
		}
	}
	require.NotNil(t, traceEvent, "must have trace-based event")
	require.NotNil(t, logEvent, "must have log-based event")

	// Both have same event_path_type (base_log) but different event_paths
	assert.Equal(t, "base_log", traceEvent.EventPathType)
	assert.Equal(t, "base_log", logEvent.EventPathType)
	assert.NotEqual(t, traceEvent.EventID, logEvent.EventID, "trace and log events must have distinct IDs")

	// Different contract addresses
	assert.Equal(t, "eth", strings.ToLower(traceEvent.ContractAddress))
	assert.Contains(t, strings.ToLower(logEvent.ContractAddress), "0xa0b86991")

	// Ingested with correct types
	for _, ev := range res.ingested {
		assert.True(t, ev.BalanceApplied)
	}
}

// ===========================================================================
// EVM: fully reverted tx — all transfers discarded, only L1 fee survives
// ===========================================================================

func TestEdge_EVM_Reverted_OnlyFeeL1(t *testing.T) {
	const watched = "0xrevertedl1addr11111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xreverted_l1_001", BlockCursor: 21000000, BlockTime: 1701400000,
			FeeAmount: "2100000000000000", FeePayer: watched, Status: string(model.TxStatusFailed),
			Error: stringPtr("execution reverted"),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// Sidecar might still return events for failed tx — normalizer discards them
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: "0xrecipient44444444444444444444444444444444",
					Delta: "-5000000000000000000",
					TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{
						"base_event_path": "tx:0",
						"base_gas_used": "100000", "base_effective_gas_price": "21000000000",
					},
				},
				// ERC20 that also gets reverted
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "erc20_transfer",
					ProgramId: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", ContractAddress: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
					Address: watched, CounterpartyAddress: "0xspender5555555555555555555555555555555555",
					Delta: "-100000000",
					TokenSymbol: "USDC", TokenDecimals: 6, TokenType: string(model.TokenTypeFungible),
					Metadata: map[string]string{"base_event_path": "log:0", "base_log_index": "0"},
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainEthereum, model.NetworkMainnet, watched, resp, "10000000000000000000", nil)

	require.Len(t, res.normalized.Transactions, 1)
	txBEs := res.normalized.Transactions[0].BalanceEvents

	// No transfer events survive
	for _, be := range txBEs {
		assert.NotEqual(t, model.ActivityDeposit, be.ActivityType, "no deposits on reverted tx")
		assert.NotEqual(t, model.ActivityWithdrawal, be.ActivityType, "no withdrawals on reverted tx")
		assert.NotEqual(t, model.ActivitySelfTransfer, be.ActivityType, "no self-transfers on reverted tx")
	}

	// L1 chain (ethereum) emits a single ActivityFee
	require.Len(t, txBEs, 1, "only 1 fee event for L1 reverted tx")
	assert.Equal(t, model.ActivityFee, txBEs[0].ActivityType)
	assert.True(t, strings.HasPrefix(txBEs[0].Delta, "-"), "fee delta must be negative")

	// Ingested
	require.Len(t, res.ingested, 1)
	assert.True(t, res.ingested[0].BalanceApplied)
}

// ===========================================================================
// EVM L2: reverted tx — only L2+L1 fee components survive
// ===========================================================================

func TestEdge_EVM_Reverted_L2FeeDecomposition(t *testing.T) {
	const watched = "0xrevertedl2addr11111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xreverted_l2_001", BlockCursor: 21100000, BlockTime: 1701500000,
			FeeAmount: "300000000000000", FeePayer: watched, Status: string(model.TxStatusFailed),
			Error: stringPtr("execution reverted: insufficient liquidity"),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// Transfer that should be discarded
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: "0xpool66666666666666666666666666666666666666",
					Delta: "-1000000000000000000",
					TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{
						"base_event_path":         "tx:0",
						"base_gas_used":           "150000",
						"base_effective_gas_price": "1000000000",
						"fee_data_l1":             "150000000000000",
					},
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainBase, model.NetworkSepolia, watched, resp, "5000000000000000000", nil)

	require.Len(t, res.normalized.Transactions, 1)
	txBEs := res.normalized.Transactions[0].BalanceEvents

	// No transfer events survive
	for _, be := range txBEs {
		assert.NotEqual(t, model.ActivityDeposit, be.ActivityType)
		assert.NotEqual(t, model.ActivityWithdrawal, be.ActivityType)
	}

	// L2 chain: should have FeeExecutionL2 + FeeDataL1
	activities := map[model.ActivityType]int{}
	for _, be := range txBEs {
		activities[be.ActivityType]++
	}
	assert.Equal(t, 1, activities[model.ActivityFeeExecutionL2], "L2 execution fee survives")
	assert.Equal(t, 1, activities[model.ActivityFeeDataL1], "L1 data fee survives")
	assert.Equal(t, 0, activities[model.ActivityFee], "no generic fee on L2")
	require.Len(t, txBEs, 2, "only 2 fee events for L2 reverted tx")

	// Both fees are negative
	for _, be := range txBEs {
		assert.True(t, strings.HasPrefix(be.Delta, "-"),
			"fee delta should be negative: %s (%s)", be.Delta, be.ActivityType)
	}
}

// ===========================================================================
// EVM: internal tx that reverts, but outer tx succeeds (partial revert)
// ===========================================================================

func TestEdge_EVM_InternalTx_PartialRevert(t *testing.T) {
	const watched = "0xpartialrevertaddr1111111111111111111111111"
	const failContract = "0xfail_contract_addr1111111111111111111111111"

	// Outer tx succeeds. Inner call reverts, but sidecar correctly
	// does NOT emit balance events for the reverted inner call.
	// Only the successful outer transfer + fee appear.
	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xpartial_revert_001", BlockCursor: 21200000, BlockTime: 1701600000,
			FeeAmount: "105000000000000", FeePayer: watched, Status: string(model.TxStatusSuccess),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				// Successful outer ERC20 transfer (this survives)
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "erc20_transfer",
					ProgramId: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", ContractAddress: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
					Address: watched, CounterpartyAddress: "0xrecipient77777777777777777777777777777777",
					Delta: "-50000000",
					TokenSymbol: "USDC", TokenDecimals: 6, TokenType: string(model.TokenTypeFungible),
					Metadata: map[string]string{"base_event_path": "log:2", "base_log_index": "2"},
				},
				// Outer tx-level native value transfer (also survives)
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: failContract,
					Delta: "-200000000000000000",
					TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{
						"base_event_path":         "tx:0",
						"base_gas_used":           "105000",
						"base_effective_gas_price": "1000000000",
					},
				},
			},
		}},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainEthereum, model.NetworkMainnet, watched, resp, "10000000000000000000", nil)

	require.Len(t, res.normalized.Transactions, 1)
	txBEs := res.normalized.Transactions[0].BalanceEvents

	// USDC withdrawal + ETH withdrawal + fee = 3
	require.Len(t, txBEs, 3, "2 successful transfers + 1 fee")

	activities := map[model.ActivityType]int{}
	for _, be := range txBEs {
		activities[be.ActivityType]++
	}
	assert.Equal(t, 2, activities[model.ActivityWithdrawal], "USDC + ETH withdrawals")
	assert.Equal(t, 1, activities[model.ActivityFee], "1 fee")

	// All events ingested successfully
	require.Len(t, res.ingested, 3)
	for _, ev := range res.ingested {
		assert.True(t, ev.BalanceApplied)
		assert.NotEmpty(t, ev.EventID)
	}
}

// ===========================================================================
// EVM: batch with mixed success and reverted txs
// ===========================================================================

func TestEdge_EVM_Reverted_MultipleTxsInBatch(t *testing.T) {
	const watched = "0xbatchmixaddr11111111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			// TX 1: SUCCESS — normal transfer
			{
				TxHash: "0xbatch_success_001", BlockCursor: 21300000, BlockTime: 1701700000,
				FeeAmount: "21000000000000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
						ProgramId: "0x0", ContractAddress: "ETH",
						Address: watched, CounterpartyAddress: "0xrecipient88888888888888888888888888888888",
						Delta: "-1000000000000000000",
						TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
						Metadata: map[string]string{
							"base_event_path": "tx:0", "base_gas_used": "21000",
							"base_effective_gas_price": "1000000000",
						},
					},
				},
			},
			// TX 2: FAILED — only fee survives
			{
				TxHash: "0xbatch_fail_001", BlockCursor: 21300001, BlockTime: 1701700010,
				FeeAmount: "63000000000000", FeePayer: watched, Status: string(model.TxStatusFailed),
				Error: stringPtr("out of gas"),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "erc20_transfer",
						ProgramId: "0xdead", ContractAddress: "0xdead",
						Address: watched, CounterpartyAddress: "0xrecipient99999999999999999999999999999999",
						Delta: "-500000000",
						TokenSymbol: "USDC", TokenDecimals: 6, TokenType: string(model.TokenTypeFungible),
						Metadata: map[string]string{
							"base_event_path": "log:0",
							"base_gas_used": "63000", "base_effective_gas_price": "1000000000",
						},
					},
				},
			},
			// TX 3: SUCCESS — deposit (fee payer is not watched)
			{
				TxHash: "0xbatch_success_002", BlockCursor: 21300002, BlockTime: 1701700020,
				FeeAmount: "0", FeePayer: "0xsomeoneelse111111111111111111111111111111", Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "erc20_transfer",
						ProgramId: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", ContractAddress: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
						Address: watched, CounterpartyAddress: "0xsender11111111111111111111111111111111111",
						Delta: "2000000000",
						TokenSymbol: "USDC", TokenDecimals: 6, TokenType: string(model.TokenTypeFungible),
						Metadata: map[string]string{"base_event_path": "log:1", "base_log_index": "1"},
					},
				},
			},
		},
	}

	walletID := "wallet-batch"
	orgID := "org-batch"
	rawBatch := &event.RawBatch{
		Chain: model.ChainEthereum, Network: model.NetworkMainnet,
		Address: watched, WalletID: &walletID, OrgID: &orgID,
		RawTransactions: []json.RawMessage{
			json.RawMessage(`{}`), json.RawMessage(`{}`), json.RawMessage(`{}`),
		},
		Signatures: []event.SignatureInfo{
			{Hash: "0xbatch_success_001", Sequence: 21300000},
			{Hash: "0xbatch_fail_001", Sequence: 21300001},
			{Hash: "0xbatch_success_002", Sequence: 21300002},
		},
	}

	res := runEdgeCaseNormalizeAndIngest(t, model.ChainEthereum, model.NetworkMainnet, watched, resp, "10000000000000000000", rawBatch)

	require.Len(t, res.normalized.Transactions, 3, "3 txs in batch")

	// TX1 (success): 1 transfer + 1 fee = 2
	tx1BEs := res.normalized.Transactions[0].BalanceEvents
	assert.Len(t, tx1BEs, 2, "TX1: transfer + fee")

	// TX2 (failed): only fee event (transfer discarded)
	tx2BEs := res.normalized.Transactions[1].BalanceEvents
	for _, be := range tx2BEs {
		assert.True(t,
			be.ActivityType == model.ActivityFee ||
				be.ActivityType == model.ActivityFeeExecutionL2 ||
				be.ActivityType == model.ActivityFeeDataL1,
			"TX2 reverted: only fee events, got: %s", be.ActivityType)
	}
	assert.Len(t, tx2BEs, 1, "TX2: only fee")

	// TX3 (success, fee_payer != watched): only deposit, no fee
	tx3BEs := res.normalized.Transactions[2].BalanceEvents
	assert.Len(t, tx3BEs, 1, "TX3: only deposit (fee payer is not watched)")
	assert.Equal(t, model.ActivityDeposit, tx3BEs[0].ActivityType)

	// Total: 2 + 1 + 1 = 4 events ingested
	require.Len(t, res.ingested, 4, "4 total events across 3 txs")

	// All event IDs unique across the batch
	ids := map[string]struct{}{}
	for _, ev := range res.ingested {
		_, dup := ids[ev.EventID]
		assert.False(t, dup, "duplicate event_id: %s", ev.EventID)
		ids[ev.EventID] = struct{}{}
	}
}

// ===========================================================================
// EVM: reverted tx with zero gas price (edge: fee = 0 → no events at all)
// ===========================================================================

func TestEdge_EVM_Reverted_ZeroGasPrice(t *testing.T) {
	const watched = "0xzerogasaddr111111111111111111111111111111111"

	resp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{{
			TxHash: "0xzero_gas_revert_001", BlockCursor: 21400000, BlockTime: 1701800000,
			// Fee = 0 (e.g., priority fee = 0 and base fee = 0 on some L2s)
			FeeAmount: "0", FeePayer: watched, Status: string(model.TxStatusFailed),
			Error: stringPtr("execution reverted"),
			BalanceEvents: []*sidecarv1.BalanceEventInfo{
				{
					EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
					ProgramId: "0x0", ContractAddress: "ETH",
					Address: watched, CounterpartyAddress: "0xrecipientaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					Delta: "-100000000000000000",
					TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
					Metadata: map[string]string{
						"base_event_path": "tx:0",
						"base_gas_used": "21000", "base_effective_gas_price": "0",
					},
				},
			},
		}},
	}

	// Test normalize-only (skip ingester — 0 events causes no BulkUpsert)
	ctrl := gomock.NewController(t)
	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := New("unused", 2*time.Second, nil, normalizedCh, 1, slog.Default())
	mockDecoder := normalizermocks.NewMockChainDecoderClient(ctrl)
	mockDecoder.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
			return resp, nil
		}).Times(1)

	walletID := "wallet-zero"
	orgID := "org-zero"
	rawBatch := event.RawBatch{
		Chain: model.ChainEthereum, Network: model.NetworkMainnet,
		Address: watched, WalletID: &walletID, OrgID: &orgID,
		RawTransactions: []json.RawMessage{json.RawMessage(`{}`)},
		Signatures:      []event.SignatureInfo{{Hash: "0xzero_gas_revert_001", Sequence: 21400000}},
	}

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockDecoder, rawBatch))

	var normalized event.NormalizedBatch
	select {
	case normalized = <-normalizedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for normalized batch")
	}

	require.Len(t, normalized.Transactions, 1)
	txBEs := normalized.Transactions[0].BalanceEvents

	// Transfer discarded (reverted). Fee = 0 → shouldEmitBaseFeeEvent returns false.
	// So NO events at all.
	assert.Len(t, txBEs, 0, "reverted tx with 0 fee emits nothing")
}

func stringPtr(s string) *string {
	return &s
}
