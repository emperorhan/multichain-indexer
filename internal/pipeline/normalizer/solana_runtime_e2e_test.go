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

type solanaFetchAdapter struct {
	signature chain.SignatureInfo
	payload   json.RawMessage
	calls     atomic.Int32
}

func (a *solanaFetchAdapter) Chain() string { return "solana" }

func (a *solanaFetchAdapter) GetHeadSequence(context.Context) (int64, error) {
	return a.signature.Sequence, nil
}

func (a *solanaFetchAdapter) FetchNewSignatures(_ context.Context, _ string, _ *string, _ int) ([]chain.SignatureInfo, error) {
	if a.calls.Add(1) == 1 {
		return []chain.SignatureInfo{a.signature}, nil
	}
	return []chain.SignatureInfo{}, nil
}

func (a *solanaFetchAdapter) FetchTransactions(_ context.Context, signatures []string) ([]json.RawMessage, error) {
	if len(signatures) != 1 || signatures[0] != a.signature.Hash {
		return nil, nil
	}
	return []json.RawMessage{a.payload}, nil
}

func TestSolanaFetchDecodeNormalizeIngestE2E(t *testing.T) {
	ctrl := gomock.NewController(t)

	const watchedAddress = "7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"
	const txSig = "5wHu1qwD7q4H3MzCTBFMoJ5nDQf7wAuCtDmGp9GHVN4y"
	const cursorSequence = int64(250000000)

	walletID := "wallet-sol-1"
	orgID := "org-sol-1"

	adapter := &solanaFetchAdapter{
		signature: chain.SignatureInfo{Hash: txSig, Sequence: cursorSequence},
		payload:   json.RawMessage(`{"slot":250000000,"blockTime":1700050000,"transaction":{"message":{"accountKeys":[{"pubkey":"7nYBpkEPkDD6m1JKBGwvftG7bHjJErJPjTH3VbKpump"}],"instructions":[]}},"meta":{"err":null,"fee":5000,"preBalances":[2000000000],"postBalances":[1999995000],"innerInstructions":[]}}`),
	}

	// --- Fetch phase ---
	jobCh := make(chan event.FetchJob, 1)
	rawBatchCh := make(chan event.RawBatch, 1)
	f := fetcher.New(adapter, jobCh, rawBatchCh, 1, slog.Default())

	fetchCtx, fetchCancel := context.WithCancel(context.Background())
	fetchErrCh := make(chan error, 1)
	go func() { fetchErrCh <- f.Run(fetchCtx) }()

	jobCh <- event.FetchJob{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
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
			assert.Equal(t, txSig, req.GetTransactions()[0].GetSignature())
			assert.Equal(t, []string{watchedAddress}, req.GetWatchedAddresses())

			return &sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					{
						TxHash:      txSig,
						BlockCursor: cursorSequence,
						BlockTime:   1700050000,
						FeeAmount:   "5000",
						FeePayer:    watchedAddress,
						Status:      string(model.TxStatusSuccess),
						BalanceEvents: []*sidecarv1.BalanceEventInfo{
							{
								OuterInstructionIndex: 0,
								InnerInstructionIndex: -1,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "system_transfer",
								ProgramId:             "11111111111111111111111111111111",
								ContractAddress:       "11111111111111111111111111111111",
								Address:               watchedAddress,
								CounterpartyAddress:   "ANotherAddr1111111111111111111111111111111",
								Delta:                 "-1000000000",
								TokenSymbol:           "SOL",
								TokenName:             "Solana",
								TokenDecimals:         9,
								TokenType:             string(model.TokenTypeNative),
								Metadata:              map[string]string{"slot": "250000000"},
							},
							{
								OuterInstructionIndex: -1,
								InnerInstructionIndex: -1,
								EventCategory:         string(model.EventCategoryFee),
								EventAction:           "transaction_fee",
								ProgramId:             "11111111111111111111111111111111",
								ContractAddress:       "11111111111111111111111111111111",
								Address:               watchedAddress,
								CounterpartyAddress:   "",
								Delta:                 "-5000",
								TokenSymbol:           "SOL",
								TokenName:             "Solana",
								TokenDecimals:         9,
								TokenType:             string(model.TokenTypeNative),
								Metadata:              map[string]string{},
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
	require.Len(t, normalized.Transactions[0].BalanceEvents, 2)

	// Verify event ID uniqueness.
	uniqueEventIDs := make(map[string]struct{}, 2)
	for _, be := range normalized.Transactions[0].BalanceEvents {
		assert.NotEmpty(t, be.EventID, "event ID should not be empty")
		_, exists := uniqueEventIDs[be.EventID]
		assert.False(t, exists, "duplicate event ID: %s", be.EventID)
		uniqueEventIDs[be.EventID] = struct{}{}
	}

	// Verify delta signs.
	for _, be := range normalized.Transactions[0].BalanceEvents {
		assert.True(t, strings.HasPrefix(be.Delta, "-"), "solana outflow delta should be negative: %s", be.Delta)
	}

	// --- Ingest phase ---
	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockTxRepo := storemocks.NewMockTransactionRepository(ctrl)
	mockBERepo := storemocks.NewMockBalanceEventRepository(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockBalanceRepo.EXPECT().
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		AnyTimes().
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "0", Exists: false}
			}
			return result, nil
		})
	mockTokenRepo := storemocks.NewMockTokenRepository(ctrl)
	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		AnyTimes().
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, contracts []string) (map[string]bool, error) {
			result := make(map[string]bool, len(contracts))
			for _, c := range contracts {
				result[c] = false
			}
			return result, nil
		})
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
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			require.Len(t, txns, 1)
			assert.Equal(t, model.ChainSolana, txns[0].Chain)
			assert.Equal(t, model.NetworkDevnet, txns[0].Network)
			assert.Equal(t, txSig, txns[0].TxHash)
			assert.Equal(t, cursorSequence, txns[0].BlockCursor)
			return map[string]uuid.UUID{txSig: txID}, nil
		}).Times(1)

	tokenID := uuid.New()
	mockTokenRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(tokens))
			for _, token := range tokens {
				assert.Equal(t, model.ChainSolana, token.Chain)
				result[token.ContractAddress] = tokenID
			}
			return result, nil
		}).Times(1)

	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			require.Len(t, events, 2)
			for _, be := range events {
				assert.Equal(t, model.ChainSolana, be.Chain)
				assert.Equal(t, txSig, be.TxHash)
				assert.NotEmpty(t, be.EventID)
			}
			return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
		}).Times(1)

	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, chain model.Chain, network model.Network, items []store.BulkAdjustItem) error {
			assert.Equal(t, model.ChainSolana, chain)
			assert.Equal(t, model.NetworkDevnet, network)
			require.NotEmpty(t, items)
			for _, item := range items {
				assert.Equal(t, watchedAddress, item.Address)
				assert.Equal(t, tokenID, item.TokenID)
			}
			return nil
		}).Times(1)

	mockCursorRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, watchedAddress, gomock.Any(), cursorSequence, int64(1)).
		Return(nil).Times(1)

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, cursorSequence).
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
