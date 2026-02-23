package normalizer

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/fetcher"
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
// Generic adapter for multi-tx event integrity tests
// ---------------------------------------------------------------------------

type integrityFetchAdapter struct {
	chainName  string
	signatures []chain.SignatureInfo
	payloads   map[string]json.RawMessage
	calls      atomic.Int32
}

func (a *integrityFetchAdapter) Chain() string { return a.chainName }

func (a *integrityFetchAdapter) GetHeadSequence(context.Context) (int64, error) {
	if len(a.signatures) == 0 {
		return 0, nil
	}
	return a.signatures[len(a.signatures)-1].Sequence, nil
}

func (a *integrityFetchAdapter) FetchNewSignatures(_ context.Context, _ string, _ *string, _ int) ([]chain.SignatureInfo, error) {
	if a.calls.Add(1) == 1 {
		return a.signatures, nil
	}
	return nil, nil
}

func (a *integrityFetchAdapter) FetchTransactions(_ context.Context, sigs []string) ([]json.RawMessage, error) {
	result := make([]json.RawMessage, 0, len(sigs))
	for _, sig := range sigs {
		if p, ok := a.payloads[sig]; ok {
			result = append(result, p)
		}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Shared helper: run Fetch → Normalize → Ingest pipeline and capture results
// ---------------------------------------------------------------------------

type integrityResult struct {
	normalizedBatch event.NormalizedBatch
	ingestedEvents  []*model.BalanceEvent
	adjustItems     []store.BulkAdjustItem
}

func runIntegrityPipeline(
	t *testing.T,
	chainID model.Chain,
	networkID model.Network,
	watchedAddr string,
	adapter chain.ChainAdapter,
	decoderResponse *sidecarv1.DecodeSolanaTransactionBatchResponse,
	initialBalance string,
) integrityResult {
	t.Helper()
	ctrl := gomock.NewController(t)

	walletID := "wallet-integrity"
	orgID := "org-integrity"

	// --- Fetch ---
	jobCh := make(chan event.FetchJob, 1)
	rawBatchCh := make(chan event.RawBatch, 1)
	f := fetcher.New(adapter, jobCh, rawBatchCh, 1, slog.Default())

	fetchCtx, fetchCancel := context.WithCancel(context.Background())
	fetchErrCh := make(chan error, 1)
	go func() { fetchErrCh <- f.Run(fetchCtx) }()

	jobCh <- event.FetchJob{
		Chain: chainID, Network: networkID,
		Address: watchedAddr, BatchSize: 10,
		WalletID: &walletID, OrgID: &orgID,
	}

	var rawBatch event.RawBatch
	select {
	case rawBatch = <-rawBatchCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for raw batch")
	}
	fetchCancel()
	<-fetchErrCh

	// --- Normalize ---
	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := New("unused", 2*time.Second, nil, normalizedCh, 1, slog.Default())
	mockDecoder := normalizermocks.NewMockChainDecoderClient(ctrl)

	mockDecoder.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
			return decoderResponse, nil
		}).Times(1)

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockDecoder, rawBatch))

	var normalized event.NormalizedBatch
	select {
	case normalized = <-normalizedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for normalized batch")
	}

	// --- Ingest ---
	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockTxRepo := storemocks.NewMockTransactionRepository(ctrl)
	mockBERepo := storemocks.NewMockBalanceEventRepository(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockTokenRepo := storemocks.NewMockTokenRepository(ctrl)
	mockConfigRepo := storemocks.NewMockWatermarkRepository(ctrl)

	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		AnyTimes().
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: initialBalance, Exists: true}
			}
			return result, nil
		})

	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		AnyTimes().
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, a := range addrs {
				result[a] = false
			}
			return result, nil
		})

	fakeDB := openE2EFakeDB(t)
	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
			return fakeDB.BeginTx(ctx, nil)
		}).Times(1)

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

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), chainID, networkID, gomock.Any()).
		Return(nil).Times(1)

	inputCh := make(chan event.NormalizedBatch, 1)
	ing := ingester.New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockConfigRepo, inputCh, slog.Default())
	inputCh <- normalized
	close(inputCh)
	require.NoError(t, ing.Run(context.Background()))

	return integrityResult{
		normalizedBatch: normalized,
		ingestedEvents:  captured,
		adjustItems:     capturedAdjust,
	}
}

// ===========================================================================
// Solana: multi-tx batch integrity
// ===========================================================================

func TestEventIntegrity_Solana_MultiTx_DepositWithdrawalFee(t *testing.T) {
	const watched = "So1anaWatchedAddr11111111111111111111111111"
	const tx1 = "sig111111111111111111111111111111111111111111"
	const tx2 = "sig222222222222222222222222222222222222222222"

	adapter := &integrityFetchAdapter{
		chainName: "solana",
		signatures: []chain.SignatureInfo{
			{Hash: tx1, Sequence: 300000001},
			{Hash: tx2, Sequence: 300000002},
		},
		payloads: map[string]json.RawMessage{
			tx1: json.RawMessage(`{"slot":300000001,"blockTime":1700050000,"transaction":{"message":{"accountKeys":[{"pubkey":"So1anaWatchedAddr11111111111111111111111111"}]}},"meta":{"err":null,"fee":5000}}`),
			tx2: json.RawMessage(`{"slot":300000002,"blockTime":1700050010,"transaction":{"message":{"accountKeys":[{"pubkey":"So1anaWatchedAddr11111111111111111111111111"}]}},"meta":{"err":null,"fee":5000}}`),
		},
	}

	decoderResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			{
				TxHash: tx1, BlockCursor: 300000001, BlockTime: 1700050000,
				FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
						ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
						Address: watched, CounterpartyAddress: "ExternalAddr111111111111111111111111111111",
						Delta: "5000000000", TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
					},
					{
						EventCategory: string(model.EventCategoryFee), EventAction: "transaction_fee",
						ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
						Address: watched, Delta: "-5000", TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
					},
				},
			},
			{
				TxHash: tx2, BlockCursor: 300000002, BlockTime: 1700050010,
				FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
						ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
						Address: watched, CounterpartyAddress: "ExternalAddr222222222222222222222222222222",
						Delta: "-3000000000", TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
					},
					{
						EventCategory: string(model.EventCategoryFee), EventAction: "transaction_fee",
						ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
						Address: watched, Delta: "-5000", TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
					},
				},
			},
		},
	}

	res := runIntegrityPipeline(t, model.ChainSolana, model.NetworkDevnet, watched, adapter, decoderResp, "10000000000")

	// --- Normalize assertions ---
	require.Len(t, res.normalizedBatch.Transactions, 2, "should normalize 2 txs")
	require.Len(t, res.normalizedBatch.Transactions[0].BalanceEvents, 2, "tx1: deposit + fee")
	require.Len(t, res.normalizedBatch.Transactions[1].BalanceEvents, 2, "tx2: withdrawal + fee")

	// --- Ingest assertions ---
	require.Len(t, res.ingestedEvents, 4, "total 4 events across 2 txs")

	eventIDs := make(map[string]struct{}, 4)
	for _, ev := range res.ingestedEvents {
		assert.Equal(t, model.ChainSolana, ev.Chain)
		assert.Equal(t, model.NetworkDevnet, ev.Network)
		assert.NotEmpty(t, ev.EventID, "every event must have event_id")
		_, dup := eventIDs[ev.EventID]
		assert.False(t, dup, "duplicate event_id: %s", ev.EventID)
		eventIDs[ev.EventID] = struct{}{}

		// BalanceApplied should be true for all (large initial balance)
		assert.True(t, ev.BalanceApplied, "event %s must have BalanceApplied=true", ev.EventID)
		assert.NotNil(t, ev.BalanceBefore, "must have BalanceBefore")
		assert.NotNil(t, ev.BalanceAfter, "must have BalanceAfter")
	}

	// Verify delta signs: tx1 has deposit(+) and fee(-), tx2 has withdrawal(-) and fee(-)
	activities := map[model.ActivityType]int{}
	for _, ev := range res.ingestedEvents {
		activities[ev.ActivityType]++
	}
	assert.Equal(t, 1, activities[model.ActivityDeposit], "1 deposit event")
	assert.Equal(t, 1, activities[model.ActivityWithdrawal], "1 withdrawal event")
	assert.Equal(t, 2, activities[model.ActivityFee], "2 fee events")

	// Balance adjustment items should be produced
	require.NotEmpty(t, res.adjustItems, "should have balance adjustments")
}

// ===========================================================================
// Ethereum (EVM): native transfer + ERC20 + fee decomposition
// ===========================================================================

func TestEventIntegrity_Ethereum_NativeAndERC20(t *testing.T) {
	const watched = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const txHash = "0xeth_integrity_tx_001"

	adapter := &integrityFetchAdapter{
		chainName:  "ethereum",
		signatures: []chain.SignatureInfo{{Hash: txHash, Sequence: 19000000}},
		payloads: map[string]json.RawMessage{
			txHash: json.RawMessage(`{"chain":"ethereum","tx":{"hash":"0xeth_integrity_tx_001"}}`),
		},
	}

	decoderResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			{
				TxHash: txHash, BlockCursor: 19000000, BlockTime: 1700200000,
				FeeAmount: "21000000000000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					// Native ETH transfer (withdrawal)
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
						ProgramId: "0x0", ContractAddress: "ETH",
						Address: watched, CounterpartyAddress: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
						Delta: "-500000000000000000", TokenSymbol: "ETH", TokenDecimals: 18,
						TokenType: string(model.TokenTypeNative),
						Metadata: map[string]string{
							"base_event_path":          "log:0",
							"base_log_index":           "0",
							"base_gas_used":            "21000",
							"base_effective_gas_price": "1000000000",
						},
					},
					// ERC20 USDC deposit
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "erc20_transfer",
						ProgramId: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", ContractAddress: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
						Address: watched, CounterpartyAddress: "0xcccccccccccccccccccccccccccccccccccccccc",
						Delta: "1000000000", TokenSymbol: "USDC", TokenName: "USD Coin", TokenDecimals: 6,
						TokenType: string(model.TokenTypeFungible),
						Metadata: map[string]string{
							"base_event_path": "log:1",
							"base_log_index":  "1",
						},
					},
				},
			},
		},
	}

	res := runIntegrityPipeline(t, model.ChainEthereum, model.NetworkMainnet, watched, adapter, decoderResp, "1000000000000000000")

	// Normalize: 1 tx with native transfer + ERC20 + fee = 3 events
	require.Len(t, res.normalizedBatch.Transactions, 1)
	require.Len(t, res.normalizedBatch.Transactions[0].BalanceEvents, 3, "native + erc20 + fee")

	// All events must have unique event_id and be base_log path type
	for _, be := range res.normalizedBatch.Transactions[0].BalanceEvents {
		assert.NotEmpty(t, be.EventID)
		assert.Equal(t, "base_log", be.EventPathType)
		assert.Equal(t, "evm-l1-decoder-v1", be.DecoderVersion)
	}

	// Ingest: verify all 3 events ingested
	require.Len(t, res.ingestedEvents, 3)

	eventIDs := make(map[string]struct{}, 3)
	activities := map[model.ActivityType]int{}
	var nativeEvt, erc20Evt, feeEvt *model.BalanceEvent
	for _, ev := range res.ingestedEvents {
		assert.Equal(t, model.ChainEthereum, ev.Chain)
		assert.Equal(t, model.NetworkMainnet, ev.Network)
		assert.True(t, ev.BalanceApplied, "all events must be applied")
		assert.NotNil(t, ev.BalanceBefore)
		assert.NotNil(t, ev.BalanceAfter)

		_, dup := eventIDs[ev.EventID]
		assert.False(t, dup, "duplicate event_id")
		eventIDs[ev.EventID] = struct{}{}
		activities[ev.ActivityType]++

		switch ev.ActivityType {
		case model.ActivityWithdrawal:
			nativeEvt = ev
		case model.ActivityDeposit:
			erc20Evt = ev
		case model.ActivityFee:
			feeEvt = ev
		}
	}

	require.NotNil(t, nativeEvt, "must have native withdrawal event")
	assert.Equal(t, "-500000000000000000", nativeEvt.Delta)
	assert.Equal(t, "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", nativeEvt.CounterpartyAddress)

	require.NotNil(t, erc20Evt, "must have ERC20 deposit event")
	assert.Equal(t, "1000000000", erc20Evt.Delta)

	require.NotNil(t, feeEvt, "must have fee event")
	assert.True(t, strings.HasPrefix(feeEvt.Delta, "-"), "fee delta must be negative")

	require.NotEmpty(t, res.adjustItems, "should produce balance adjustments")
}

// ===========================================================================
// BTC: multi-input multi-output UTXO
// ===========================================================================

func TestEventIntegrity_BTC_MultiInputMultiOutput(t *testing.T) {
	const watched = "bc1qwatchedaddr1111111111111111111111"
	const txHash = "btc_integrity_txid_001"

	adapter := &integrityFetchAdapter{
		chainName:  "btc",
		signatures: []chain.SignatureInfo{{Hash: txHash, Sequence: 830000}},
		payloads: map[string]json.RawMessage{
			txHash: json.RawMessage(`{"chain":"btc","txid":"btc_integrity_txid_001","block_height":830000}`),
		},
	}

	// 2 vin_spend (watched spent 2 UTXOs) + 1 vout_receive (change) + miner_fee
	decoderResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			{
				TxHash: txHash, BlockCursor: 830000, BlockTime: 1700300000,
				FeeAmount: "3000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "vin_spend",
						ProgramId: "btc", ContractAddress: "BTC",
						Address: watched, Delta: "-100000",
						TokenSymbol: "BTC", TokenDecimals: 8, TokenType: string(model.TokenTypeNative),
						Metadata: map[string]string{
							"event_path": "vin:0", "utxo_path_type": "vin",
							"input_txid": "prev-tx-aaa", "input_vout": "0", "finality_state": "confirmed",
						},
					},
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "vin_spend",
						ProgramId: "btc", ContractAddress: "BTC",
						Address: watched, Delta: "-50000",
						TokenSymbol: "BTC", TokenDecimals: 8, TokenType: string(model.TokenTypeNative),
						Metadata: map[string]string{
							"event_path": "vin:1", "utxo_path_type": "vin",
							"input_txid": "prev-tx-bbb", "input_vout": "1", "finality_state": "confirmed",
						},
					},
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "vout_receive",
						ProgramId: "btc", ContractAddress: "BTC",
						Address: watched, Delta: "97000",
						TokenSymbol: "BTC", TokenDecimals: 8, TokenType: string(model.TokenTypeNative),
						Metadata: map[string]string{
							"event_path": "vout:1", "utxo_path_type": "vout", "finality_state": "confirmed",
						},
					},
				},
			},
		},
	}

	res := runIntegrityPipeline(t, model.ChainBTC, model.NetworkMainnet, watched, adapter, decoderResp, "200000")

	// Normalize: 1 tx with 2 vin + 1 vout + 1 miner_fee = 4 events
	require.Len(t, res.normalizedBatch.Transactions, 1)
	require.Len(t, res.normalizedBatch.Transactions[0].BalanceEvents, 4, "2 vin + 1 vout + 1 fee")

	// Event ID uniqueness
	eventIDs := make(map[string]struct{}, 4)
	for _, be := range res.normalizedBatch.Transactions[0].BalanceEvents {
		assert.NotEmpty(t, be.EventID)
		_, dup := eventIDs[be.EventID]
		assert.False(t, dup, "duplicate event_id: %s", be.EventID)
		eventIDs[be.EventID] = struct{}{}
	}

	// Classify events
	var vinEvents, voutEvents, feeEvents []event.NormalizedBalanceEvent
	for _, be := range res.normalizedBatch.Transactions[0].BalanceEvents {
		switch be.EventAction {
		case "vin_spend":
			vinEvents = append(vinEvents, be)
		case "vout_receive":
			voutEvents = append(voutEvents, be)
		case "miner_fee":
			feeEvents = append(feeEvents, be)
		}
	}
	assert.Len(t, vinEvents, 2, "2 vin_spend events")
	assert.Len(t, voutEvents, 1, "1 vout_receive event")
	assert.Len(t, feeEvents, 1, "1 miner_fee event")

	// Verify delta signs
	for _, v := range vinEvents {
		assert.True(t, strings.HasPrefix(v.Delta, "-"), "vin_spend must be negative: %s", v.Delta)
	}
	for _, v := range voutEvents {
		assert.False(t, strings.HasPrefix(v.Delta, "-"), "vout_receive must be positive: %s", v.Delta)
	}
	assert.Equal(t, "-3000", feeEvents[0].Delta, "miner_fee delta")

	// Finality state
	for _, be := range res.normalizedBatch.Transactions[0].BalanceEvents {
		assert.Equal(t, "confirmed", be.FinalityState, "BTC default finality")
	}

	// Ingest: all 4 events applied
	require.Len(t, res.ingestedEvents, 4)
	for _, ev := range res.ingestedEvents {
		assert.Equal(t, model.ChainBTC, ev.Chain)
		assert.True(t, ev.BalanceApplied, "event %s must be applied", ev.EventID)
		assert.NotNil(t, ev.BalanceBefore)
		assert.NotNil(t, ev.BalanceAfter)
	}
}

// ===========================================================================
// Negative balance: events still applied (post-migration 026)
// ===========================================================================

func TestEventIntegrity_Solana_NegativeBalance_StillApplied(t *testing.T) {
	const watched = "NegBalWatchedAddr1111111111111111111111111"
	const txHash = "negbalsig111111111111111111111111111111111111"

	adapter := &integrityFetchAdapter{
		chainName:  "solana",
		signatures: []chain.SignatureInfo{{Hash: txHash, Sequence: 400000000}},
		payloads: map[string]json.RawMessage{
			txHash: json.RawMessage(`{"slot":400000000,"blockTime":1700060000,"transaction":{"message":{"accountKeys":[{"pubkey":"NegBalWatchedAddr1111111111111111111111111"}]}},"meta":{"err":null,"fee":5000}}`),
		},
	}

	decoderResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			{
				TxHash: txHash, BlockCursor: 400000000, BlockTime: 1700060000,
				FeeAmount: "5000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
						ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
						Address: watched, CounterpartyAddress: "ExtAddr111111111111111111111111111111111111",
						Delta: "-5000000000", TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
					},
					{
						EventCategory: string(model.EventCategoryFee), EventAction: "transaction_fee",
						ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
						Address: watched, Delta: "-5000", TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
					},
				},
			},
		},
	}

	// Initial balance 1000 SOL-lamports, withdrawal -5B → negative result
	res := runIntegrityPipeline(t, model.ChainSolana, model.NetworkDevnet, watched, adapter, decoderResp, "1000")

	require.Len(t, res.ingestedEvents, 2)

	// All events must still be applied even with negative balance
	for _, ev := range res.ingestedEvents {
		assert.True(t, ev.BalanceApplied, "negative balance events must still be applied (post-migration 026)")
		assert.NotNil(t, ev.BalanceBefore)
		assert.NotNil(t, ev.BalanceAfter)

		// Verify the balance went negative
		if ev.ActivityType == model.ActivityWithdrawal {
			assert.True(t, strings.HasPrefix(*ev.BalanceAfter, "-"),
				"withdrawal from small balance should produce negative BalanceAfter: got %s", *ev.BalanceAfter)
		}
	}

	// Balance adjustments should still be produced
	require.NotEmpty(t, res.adjustItems, "negative balance should still produce adjustments")
}

func TestEventIntegrity_EVM_NegativeBalance_StillApplied(t *testing.T) {
	const watched = "0xdddddddddddddddddddddddddddddddddddddd"
	const txHash = "0xneg_evm_001"

	adapter := &integrityFetchAdapter{
		chainName:  "base",
		signatures: []chain.SignatureInfo{{Hash: txHash, Sequence: 50000}},
		payloads: map[string]json.RawMessage{
			txHash: json.RawMessage(`{"chain":"base","tx":{"hash":"0xneg_evm_001"}}`),
		},
	}

	decoderResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			{
				TxHash: txHash, BlockCursor: 50000, BlockTime: 1700400000,
				FeeAmount: "100000000000000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
						ProgramId: "0x0", ContractAddress: "ETH",
						Address: watched, CounterpartyAddress: "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
						Delta: "-900000000000000000", TokenSymbol: "ETH", TokenDecimals: 18,
						TokenType: string(model.TokenTypeNative),
						Metadata: map[string]string{
							"base_event_path": "log:0", "base_log_index": "0",
							"base_gas_used": "50000", "base_effective_gas_price": "2000000000",
							"fee_data_l1": "10000",
						},
					},
				},
			},
		},
	}

	// Initial balance 100 wei — far less than 0.9 ETH withdrawal → negative
	res := runIntegrityPipeline(t, model.ChainBase, model.NetworkSepolia, watched, adapter, decoderResp, "100")

	// Should have transfer + L2 fee + L1 fee = 3 events
	require.Len(t, res.ingestedEvents, 3)

	for _, ev := range res.ingestedEvents {
		assert.True(t, ev.BalanceApplied, "negative balance EVM event must still be applied")
		assert.NotNil(t, ev.BalanceBefore)
		assert.NotNil(t, ev.BalanceAfter)
	}
}

func TestEventIntegrity_BTC_NegativeBalance_StillApplied(t *testing.T) {
	const watched = "bc1qnegbalwatched1111111111111111111"
	const txHash = "btc_neg_txid_001"

	adapter := &integrityFetchAdapter{
		chainName:  "btc",
		signatures: []chain.SignatureInfo{{Hash: txHash, Sequence: 840000}},
		payloads: map[string]json.RawMessage{
			txHash: json.RawMessage(`{"chain":"btc","txid":"btc_neg_txid_001","block_height":840000}`),
		},
	}

	decoderResp := &sidecarv1.DecodeSolanaTransactionBatchResponse{
		Results: []*sidecarv1.TransactionResult{
			{
				TxHash: txHash, BlockCursor: 840000, BlockTime: 1700500000,
				FeeAmount: "1000", FeePayer: watched, Status: string(model.TxStatusSuccess),
				BalanceEvents: []*sidecarv1.BalanceEventInfo{
					{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "vin_spend",
						ProgramId: "btc", ContractAddress: "BTC",
						Address: watched, Delta: "-500000",
						TokenSymbol: "BTC", TokenDecimals: 8, TokenType: string(model.TokenTypeNative),
						Metadata: map[string]string{
							"event_path": "vin:0", "utxo_path_type": "vin",
							"input_txid": "prev-tx-neg", "input_vout": "0", "finality_state": "confirmed",
						},
					},
				},
			},
		},
	}

	// Initial balance 100 satoshis, spend 500000 → massively negative
	res := runIntegrityPipeline(t, model.ChainBTC, model.NetworkMainnet, watched, adapter, decoderResp, "100")

	// vin_spend + miner_fee = 2 events
	require.Len(t, res.ingestedEvents, 2)

	for _, ev := range res.ingestedEvents {
		assert.True(t, ev.BalanceApplied, "BTC negative balance event must still be applied")
		assert.NotNil(t, ev.BalanceBefore)
		assert.NotNil(t, ev.BalanceAfter)
	}

	// Verify negative after balance
	var vinEvt *model.BalanceEvent
	for _, ev := range res.ingestedEvents {
		if strings.Contains(ev.EventAction, "vin_spend") || ev.Delta == "-500000" {
			vinEvt = ev
			break
		}
	}
	require.NotNil(t, vinEvt, "must have vin_spend event")
	assert.True(t, strings.HasPrefix(*vinEvt.BalanceAfter, "-"),
		"BTC vin_spend from small balance should go negative: got %s", *vinEvt.BalanceAfter)
}

// ===========================================================================
// Event ID determinism: same input → same event_id
// ===========================================================================

func TestEventIntegrity_EventID_Determinism_AllChains(t *testing.T) {
	type chainSetup struct {
		name     string
		chainID  model.Chain
		network  model.Network
		watched  string
		adapter  *integrityFetchAdapter
		response *sidecarv1.DecodeSolanaTransactionBatchResponse
	}

	setups := []chainSetup{
		{
			name: "solana", chainID: model.ChainSolana, network: model.NetworkDevnet,
			watched: "DetAddr1111111111111111111111111111111111111",
			adapter: &integrityFetchAdapter{
				chainName:  "solana",
				signatures: []chain.SignatureInfo{{Hash: "detsig1111", Sequence: 500000000}},
				payloads:   map[string]json.RawMessage{"detsig1111": json.RawMessage(`{"slot":500000000,"blockTime":1700070000,"transaction":{"message":{"accountKeys":[{"pubkey":"DetAddr1111111111111111111111111111111111111"}]}},"meta":{"err":null,"fee":5000}}`)},
			},
			response: &sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{{
					TxHash: "detsig1111", BlockCursor: 500000000, BlockTime: 1700070000,
					FeeAmount: "5000", FeePayer: "DetAddr1111111111111111111111111111111111111", Status: string(model.TxStatusSuccess),
					BalanceEvents: []*sidecarv1.BalanceEventInfo{{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "system_transfer",
						ProgramId: "11111111111111111111111111111111", ContractAddress: "11111111111111111111111111111111",
						Address: "DetAddr1111111111111111111111111111111111111", Delta: "1000000",
						TokenSymbol: "SOL", TokenDecimals: 9, TokenType: string(model.TokenTypeNative),
					}},
				}},
			},
		},
		{
			name: "ethereum", chainID: model.ChainEthereum, network: model.NetworkMainnet,
			watched: "0xdddddddddddddddddddddddddddddddddddddd",
			adapter: &integrityFetchAdapter{
				chainName:  "ethereum",
				signatures: []chain.SignatureInfo{{Hash: "0xdet_eth_001", Sequence: 20000000}},
				payloads:   map[string]json.RawMessage{"0xdet_eth_001": json.RawMessage(`{"chain":"ethereum","tx":{"hash":"0xdet_eth_001"}}`)},
			},
			response: &sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{{
					TxHash: "0xdet_eth_001", BlockCursor: 20000000, BlockTime: 1700600000,
					FeeAmount: "21000000000000", FeePayer: "0xdddddddddddddddddddddddddddddddddddddd", Status: string(model.TxStatusSuccess),
					BalanceEvents: []*sidecarv1.BalanceEventInfo{{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "native_transfer",
						ProgramId: "0x0", ContractAddress: "ETH",
						Address: "0xdddddddddddddddddddddddddddddddddddddd", Delta: "-100000000000000000",
						TokenSymbol: "ETH", TokenDecimals: 18, TokenType: string(model.TokenTypeNative),
						Metadata: map[string]string{"base_event_path": "log:0", "base_log_index": "0", "base_gas_used": "21000", "base_effective_gas_price": "1000000000"},
					}},
				}},
			},
		},
		{
			name: "btc", chainID: model.ChainBTC, network: model.NetworkMainnet,
			watched: "bc1qdetaddr111111111111111111111111111",
			adapter: &integrityFetchAdapter{
				chainName:  "btc",
				signatures: []chain.SignatureInfo{{Hash: "det_btc_txid", Sequence: 850000}},
				payloads:   map[string]json.RawMessage{"det_btc_txid": json.RawMessage(`{"chain":"btc","txid":"det_btc_txid","block_height":850000}`)},
			},
			response: &sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{{
					TxHash: "det_btc_txid", BlockCursor: 850000, BlockTime: 1700700000,
					FeeAmount: "2000", FeePayer: "bc1qdetaddr111111111111111111111111111", Status: string(model.TxStatusSuccess),
					BalanceEvents: []*sidecarv1.BalanceEventInfo{{
						EventCategory: string(model.EventCategoryTransfer), EventAction: "vout_receive",
						ProgramId: "btc", ContractAddress: "BTC",
						Address: "bc1qdetaddr111111111111111111111111111", Delta: "50000",
						TokenSymbol: "BTC", TokenDecimals: 8, TokenType: string(model.TokenTypeNative),
						Metadata: map[string]string{"event_path": "vout:0", "utxo_path_type": "vout", "finality_state": "confirmed"},
					}},
				}},
			},
		},
	}

	for _, setup := range setups {
		t.Run(setup.name, func(t *testing.T) {
			// Run twice with fresh adapters (reset call counters)
			setup.adapter.calls.Store(0)
			res1 := runIntegrityPipeline(t, setup.chainID, setup.network, setup.watched, setup.adapter, setup.response, "1000000000")

			setup.adapter.calls.Store(0)
			res2 := runIntegrityPipeline(t, setup.chainID, setup.network, setup.watched, setup.adapter, setup.response, "1000000000")

			// Same number of events
			require.Equal(t, len(res1.normalizedBatch.Transactions), len(res2.normalizedBatch.Transactions))

			// Event IDs must be identical across runs
			ids1 := make([]string, 0)
			ids2 := make([]string, 0)
			for _, tx := range res1.normalizedBatch.Transactions {
				for _, be := range tx.BalanceEvents {
					ids1 = append(ids1, be.EventID)
				}
			}
			for _, tx := range res2.normalizedBatch.Transactions {
				for _, be := range tx.BalanceEvents {
					ids2 = append(ids2, be.EventID)
				}
			}

			require.Equal(t, len(ids1), len(ids2), "event count mismatch between runs")
			for i := range ids1 {
				assert.Equal(t, ids1[i], ids2[i], "event_id[%d] differs between runs", i)
			}
		})
	}
}

// ===========================================================================
// Empty batch: sentinel (0-tx) should not create events
// ===========================================================================

func TestEventIntegrity_EmptyBatch_AllChains(t *testing.T) {
	chains := []struct {
		name    string
		chainID model.Chain
		network model.Network
		watched string
	}{
		{"solana", model.ChainSolana, model.NetworkDevnet, "EmptyAddr1111111111111111111111111111111111"},
		{"ethereum", model.ChainEthereum, model.NetworkMainnet, "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},
		{"btc", model.ChainBTC, model.NetworkMainnet, "bc1qemptyaddr111111111111111111111111"},
	}

	for _, c := range chains {
		t.Run(c.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			normalizedCh := make(chan event.NormalizedBatch, 1)
			n := New("unused", 2*time.Second, nil, normalizedCh, 1, slog.Default())
			mockDecoder := normalizermocks.NewMockChainDecoderClient(ctrl)

			// Empty batches short-circuit before calling sidecar (sentinel path)
			mockDecoder.EXPECT().
				DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
					return &sidecarv1.DecodeSolanaTransactionBatchResponse{Results: nil}, nil
				}).MaxTimes(1)

			walletID := "w"
			orgID := "o"
			rawBatch := event.RawBatch{
				Chain: c.chainID, Network: c.network,
				Address: c.watched, WalletID: &walletID, OrgID: &orgID,
				RawTransactions: nil, // empty
			}

			require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockDecoder, rawBatch))

			var normalized event.NormalizedBatch
			select {
			case normalized = <-normalizedCh:
			case <-time.After(2 * time.Second):
				t.Fatal("timeout waiting for normalized batch")
			}

			assert.Empty(t, normalized.Transactions, "empty batch should produce 0 transactions")
		})
	}
}
