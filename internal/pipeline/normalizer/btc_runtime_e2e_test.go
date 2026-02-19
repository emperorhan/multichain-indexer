package normalizer

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
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

type btcFetchAdapter struct {
	signature chain.SignatureInfo
	payload   json.RawMessage
	calls     atomic.Int32
}

func (a *btcFetchAdapter) Chain() string { return "btc" }

func (a *btcFetchAdapter) GetHeadSequence(context.Context) (int64, error) {
	return a.signature.Sequence, nil
}

func (a *btcFetchAdapter) FetchNewSignatures(_ context.Context, _ string, _ *string, _ int) ([]chain.SignatureInfo, error) {
	if a.calls.Add(1) == 1 {
		return []chain.SignatureInfo{a.signature}, nil
	}
	return []chain.SignatureInfo{}, nil
}

func (a *btcFetchAdapter) FetchTransactions(_ context.Context, signatures []string) ([]json.RawMessage, error) {
	if len(signatures) != 1 || signatures[0] != a.signature.Hash {
		return nil, nil
	}
	return []json.RawMessage{a.payload}, nil
}

func TestBTCFetchDecodeNormalizeIngestE2E(t *testing.T) {
	ctrl := gomock.NewController(t)

	const watchedAddress = "tb1qwatched111111111111111111111111"
	const txHash = "a1b2c3d4e5f6"
	const cursorSequence = int64(800000)

	walletID := "wallet-btc-1"
	orgID := "org-btc-1"

	btcPayload := json.RawMessage(`{
		"chain":"btc",
		"txid":"a1b2c3d4e5f6",
		"block_height":800000,
		"block_time":1700100000,
		"confirmations":6,
		"fee_sat":"1500",
		"fee_payer":"tb1qwatched111111111111111111111111",
		"vin":[{"index":0,"txid":"prev-tx-001","vout":0,"address":"tb1qwatched111111111111111111111111","value_sat":"50000"}],
		"vout":[{"index":0,"address":"tb1qexternal22222222222222222222222","value_sat":"30000"},{"index":1,"address":"tb1qwatched111111111111111111111111","value_sat":"18500"}]
	}`)

	adapter := &btcFetchAdapter{
		signature: chain.SignatureInfo{Hash: txHash, Sequence: cursorSequence},
		payload:   btcPayload,
	}

	// --- Fetch phase ---
	jobCh := make(chan event.FetchJob, 1)
	rawBatchCh := make(chan event.RawBatch, 1)
	f := fetcher.New(adapter, jobCh, rawBatchCh, 1, slog.Default())

	fetchCtx, fetchCancel := context.WithCancel(context.Background())
	fetchErrCh := make(chan error, 1)
	go func() { fetchErrCh <- f.Run(fetchCtx) }()

	jobCh <- event.FetchJob{
		Chain:     model.ChainBTC,
		Network:   model.NetworkMainnet,
		Address:   watchedAddress,
		BatchSize: 10,
		WalletID:  &walletID,
		OrgID:     &orgID,
	}

	var rawBatch event.RawBatch
	select {
	case rawBatch = <-rawBatchCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fetched raw batch")
	}
	fetchCancel()
	assert.ErrorIs(t, <-fetchErrCh, context.Canceled)

	// --- Normalize phase ---
	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := New("unused", 2*time.Second, nil, normalizedCh, 1, slog.Default())
	mockDecoder := normalizermocks.NewMockChainDecoderClient(ctrl)

	mockDecoder.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
			require.Len(t, req.GetTransactions(), 1)
			assert.Equal(t, txHash, req.GetTransactions()[0].GetSignature())

			return &sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					{
						TxHash:      txHash,
						BlockCursor: cursorSequence,
						BlockTime:   1700100000,
						FeeAmount:   "1500",
						FeePayer:    watchedAddress,
						Status:      string(model.TxStatusSuccess),
						BalanceEvents: []*sidecarv1.BalanceEventInfo{
							{
								OuterInstructionIndex: 0,
								InnerInstructionIndex: -1,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "vin_spend",
								ProgramId:             "btc",
								ContractAddress:       "BTC",
								Address:               watchedAddress,
								CounterpartyAddress:   "",
								Delta:                 "-50000",
								TokenSymbol:           "BTC",
								TokenName:             "Bitcoin",
								TokenDecimals:         8,
								TokenType:             string(model.TokenTypeNative),
								Metadata: map[string]string{
									"event_path":     "vin:0",
									"utxo_path_type": "vin",
									"input_txid":     "prev-tx-001",
									"input_vout":     "0",
									"finality_state": "confirmed",
								},
							},
							{
								OuterInstructionIndex: 1,
								InnerInstructionIndex: -1,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "vout_receive",
								ProgramId:             "btc",
								ContractAddress:       "BTC",
								Address:               watchedAddress,
								CounterpartyAddress:   "",
								Delta:                 "18500",
								TokenSymbol:           "BTC",
								TokenName:             "Bitcoin",
								TokenDecimals:         8,
								TokenType:             string(model.TokenTypeNative),
								Metadata: map[string]string{
									"event_path":     "vout:1",
									"utxo_path_type": "vout",
									"finality_state": "confirmed",
								},
							},
						},
					},
				},
			}, nil
		}).Times(1)

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockDecoder, rawBatch))

	var normalized event.NormalizedBatch
	select {
	case normalized = <-normalizedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for normalized batch")
	}

	require.Len(t, normalized.Transactions, 1)
	require.Len(t, normalized.Transactions[0].BalanceEvents, 3)

	// Verify event ID uniqueness.
	uniqueEventIDs := make(map[string]struct{}, 3)
	for _, be := range normalized.Transactions[0].BalanceEvents {
		assert.NotEmpty(t, be.EventID)
		_, exists := uniqueEventIDs[be.EventID]
		assert.False(t, exists, "duplicate event ID: %s", be.EventID)
		uniqueEventIDs[be.EventID] = struct{}{}
	}

	// Verify UTXO delta signs: vin_spend negative, vout_receive positive.
	var vinEvent, voutEvent, feeEvent *event.NormalizedBalanceEvent
	for i := range normalized.Transactions[0].BalanceEvents {
		be := &normalized.Transactions[0].BalanceEvents[i]
		if be.EventAction == "vin_spend" {
			vinEvent = be
		} else if be.EventAction == "vout_receive" {
			voutEvent = be
		} else if be.ActivityType == model.ActivityFee {
			feeEvent = be
		}
	}
	require.NotNil(t, vinEvent, "should have vin_spend event")
	require.NotNil(t, voutEvent, "should have vout_receive event")
	require.NotNil(t, feeEvent, "should have miner_fee event")
	assert.Equal(t, "-50000", vinEvent.Delta, "vin_spend should be negative")
	assert.Equal(t, "18500", voutEvent.Delta, "vout_receive should be positive")
	assert.Equal(t, "miner_fee", feeEvent.EventAction)
	assert.Equal(t, "-1500", feeEvent.Delta)

	// Verify fee metadata.
	assert.Equal(t, "confirmed", vinEvent.FinalityState)

	// --- Ingest phase ---
	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockTxRepo := storemocks.NewMockTransactionRepository(ctrl)
	mockBERepo := storemocks.NewMockBalanceEventRepository(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockBalanceRepo.EXPECT().
		GetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		AnyTimes().Return("0", false, nil)
	mockTokenRepo := storemocks.NewMockTokenRepository(ctrl)
	mockTokenRepo.EXPECT().
		IsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		AnyTimes().
		Return(false, nil)
	mockCursorRepo := storemocks.NewMockCursorRepository(ctrl)
	mockConfigRepo := storemocks.NewMockIndexerConfigRepository(ctrl)

	fakeDB := openE2EFakeDB(t)
	mockDB.EXPECT().
		BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
			return fakeDB.BeginTx(ctx, nil)
		}).Times(1)

	txID := uuid.New()
	mockTxRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tx *model.Transaction) (uuid.UUID, error) {
			assert.Equal(t, model.ChainBTC, tx.Chain)
			assert.Equal(t, model.NetworkMainnet, tx.Network)
			assert.Equal(t, cursorSequence, tx.BlockCursor)
			return txID, nil
		}).Times(1)

	tokenID := uuid.New()
	mockTokenRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, token *model.Token) (uuid.UUID, error) {
			assert.Equal(t, model.ChainBTC, token.Chain)
			assert.Equal(t, "BTC", token.ContractAddress)
			return tokenID, nil
		}).Times(3)

	mockBERepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (store.UpsertResult, error) {
			assert.Equal(t, model.ChainBTC, be.Chain)
			assert.NotEmpty(t, be.EventID)
			return store.UpsertResult{Inserted: true}, nil
		}).Times(3)

	mockBalanceRepo.EXPECT().
		AdjustBalanceTx(gomock.Any(), gomock.Any(), model.ChainBTC, model.NetworkMainnet, watchedAddress, tokenID, &walletID, &orgID, gomock.Any(), cursorSequence, gomock.Any(), "").
		Return(nil).Times(3)

	mockCursorRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), model.ChainBTC, model.NetworkMainnet, watchedAddress, gomock.Any(), cursorSequence, int64(1)).
		Return(nil).Times(1)

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainBTC, model.NetworkMainnet, cursorSequence).
		Return(nil).Times(1)

	ingestInputCh := make(chan event.NormalizedBatch, 1)
	ing := ingester.New(
		mockDB, mockTxRepo, mockBERepo, mockBalanceRepo,
		mockTokenRepo, mockCursorRepo, mockConfigRepo,
		ingestInputCh, slog.Default(),
	)

	ingestInputCh <- normalized
	close(ingestInputCh)
	require.NoError(t, ing.Run(context.Background()))
}
