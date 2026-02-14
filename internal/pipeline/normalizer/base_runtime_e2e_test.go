package normalizer

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/fetcher"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/ingester"
	normalizermocks "github.com/emperorhan/multichain-indexer/internal/pipeline/normalizer/mocks"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
)

type e2eFakeDriver struct{}
type e2eFakeConn struct{}
type e2eFakeTx struct{}

func (d *e2eFakeDriver) Open(name string) (driver.Conn, error) { return &e2eFakeConn{}, nil }
func (c *e2eFakeConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}
func (c *e2eFakeConn) Close() error              { return nil }
func (c *e2eFakeConn) Begin() (driver.Tx, error) { return &e2eFakeTx{}, nil }
func (tx *e2eFakeTx) Commit() error              { return nil }
func (tx *e2eFakeTx) Rollback() error            { return nil }

var registerE2EFakeDriver sync.Once

func openE2EFakeDB(t *testing.T) *sql.DB {
	t.Helper()
	registerE2EFakeDriver.Do(func() {
		sql.Register("fake_normalizer_e2e_ingester", &e2eFakeDriver{})
	})
	db, err := sql.Open("fake_normalizer_e2e_ingester", "")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

type baseFetchAdapter struct {
	signature chain.SignatureInfo
	payload   json.RawMessage
	calls     atomic.Int32
}

func (a *baseFetchAdapter) Chain() string {
	return "base"
}

func (a *baseFetchAdapter) GetHeadSequence(context.Context) (int64, error) {
	return a.signature.Sequence, nil
}

func (a *baseFetchAdapter) FetchNewSignatures(_ context.Context, _ string, _ *string, _ int) ([]chain.SignatureInfo, error) {
	if a.calls.Add(1) == 1 {
		return []chain.SignatureInfo{a.signature}, nil
	}
	return []chain.SignatureInfo{}, nil
}

func (a *baseFetchAdapter) FetchTransactions(_ context.Context, signatures []string) ([]json.RawMessage, error) {
	if len(signatures) != 1 || signatures[0] != a.signature.Hash {
		return nil, fmt.Errorf("unexpected signatures: %v", signatures)
	}
	return []json.RawMessage{a.payload}, nil
}

func TestBaseSepoliaFetchDecodeNormalizeIngestE2E(t *testing.T) {
	ctrl := gomock.NewController(t)

	const watchedAddress = "0x1111111111111111111111111111111111111111"
	const txHash = "0xabc123"
	const cursorSequence = int64(123)

	walletID := "wallet-base-1"
	orgID := "org-base-1"

	adapter := &baseFetchAdapter{
		signature: chain.SignatureInfo{Hash: txHash, Sequence: cursorSequence},
		payload:   json.RawMessage(`{"chain":"base","tx":{"hash":"0xabc123"},"receipt":{"transactionHash":"0xabc123"}}`),
	}

	jobCh := make(chan event.FetchJob, 1)
	rawBatchCh := make(chan event.RawBatch, 1)
	f := fetcher.New(adapter, jobCh, rawBatchCh, 1, slog.Default())

	fetchCtx, fetchCancel := context.WithCancel(context.Background())
	fetchErrCh := make(chan error, 1)
	go func() {
		fetchErrCh <- f.Run(fetchCtx)
	}()

	jobCh <- event.FetchJob{
		Chain:     model.ChainBase,
		Network:   model.NetworkSepolia,
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

	normalizedCh := make(chan event.NormalizedBatch, 1)
	n := New("unused", 2*time.Second, nil, normalizedCh, 1, slog.Default())
	mockDecoder := normalizermocks.NewMockChainDecoderClient(ctrl)

	mockDecoder.EXPECT().
		DecodeSolanaTransactionBatch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *sidecarv1.DecodeSolanaTransactionBatchRequest, _ ...grpc.CallOption) (*sidecarv1.DecodeSolanaTransactionBatchResponse, error) {
			require.Len(t, req.GetTransactions(), 1)
			assert.Equal(t, txHash, req.GetTransactions()[0].GetSignature())
			assert.Equal(t, []string{watchedAddress}, req.GetWatchedAddresses())

			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal(req.GetTransactions()[0].GetRawJson(), &payload))
			assert.Equal(t, "base", payload["chain"])

			return &sidecarv1.DecodeSolanaTransactionBatchResponse{
				Results: []*sidecarv1.TransactionResult{
					{
						TxHash:      txHash,
						BlockCursor: cursorSequence,
						BlockTime:   1700000000,
						FeeAmount:   "21000000030000",
						FeePayer:    watchedAddress,
						Status:      string(model.TxStatusSuccess),
						BalanceEvents: []*sidecarv1.BalanceEventInfo{
							{
								OuterInstructionIndex: 7,
								InnerInstructionIndex: -1,
								EventCategory:         string(model.EventCategoryTransfer),
								EventAction:           "native_transfer",
								ProgramId:             "0xbase-program",
								ContractAddress:       "ETH",
								Address:               watchedAddress,
								CounterpartyAddress:   "0x2222222222222222222222222222222222222222",
								Delta:                 "-100000000000000000",
								TokenSymbol:           "ETH",
								TokenName:             "Ether",
								TokenDecimals:         18,
								TokenType:             string(model.TokenTypeNative),
								Metadata: map[string]string{
									"base_event_path":          "log:7",
									"base_log_index":           "7",
									"base_gas_used":            "21000",
									"base_effective_gas_price": "1000000000",
									"fee_data_l1":              "30000",
								},
							},
						},
					},
				},
			}, nil
		}).
		Times(1)

	require.NoError(t, n.processBatch(context.Background(), slog.Default(), mockDecoder, rawBatch))

	var normalized event.NormalizedBatch
	select {
	case normalized = <-normalizedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for normalized batch")
	}

	require.Len(t, normalized.Transactions, 1)
	require.Len(t, normalized.Transactions[0].BalanceEvents, 3)

	uniqueEventIDs := make(map[string]struct{}, 3)
	for _, be := range normalized.Transactions[0].BalanceEvents {
		assert.NotEmpty(t, be.EventID)
		assert.Equal(t, "base_log", be.EventPathType)
		assert.Equal(t, "base-decoder-v1", be.DecoderVersion)
		_, exists := uniqueEventIDs[be.EventID]
		assert.False(t, exists, "duplicate canonical event id found: %s", be.EventID)
		uniqueEventIDs[be.EventID] = struct{}{}
	}

	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockTxRepo := storemocks.NewMockTransactionRepository(ctrl)
	mockBERepo := storemocks.NewMockBalanceEventRepository(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockTokenRepo := storemocks.NewMockTokenRepository(ctrl)
	mockCursorRepo := storemocks.NewMockCursorRepository(ctrl)
	mockConfigRepo := storemocks.NewMockIndexerConfigRepository(ctrl)

	fakeDB := openE2EFakeDB(t)
	mockDB.EXPECT().
		BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
			return fakeDB.BeginTx(ctx, nil)
		}).
		Times(1)

	txID := uuid.New()
	mockTxRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tx *model.Transaction) (uuid.UUID, error) {
			assert.Equal(t, model.ChainBase, tx.Chain)
			assert.Equal(t, model.NetworkSepolia, tx.Network)
			assert.Equal(t, txHash, tx.TxHash)
			assert.Equal(t, watchedAddress, tx.FeePayer)
			assert.Equal(t, cursorSequence, tx.BlockCursor)
			return txID, nil
		}).
		Times(1)

	tokenID := uuid.New()
	mockTokenRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, token *model.Token) (uuid.UUID, error) {
			assert.Equal(t, model.ChainBase, token.Chain)
			assert.Equal(t, model.NetworkSepolia, token.Network)
			assert.Equal(t, "ETH", token.ContractAddress)
			return tokenID, nil
		}).
		Times(3)

	ingestedCategories := make(map[model.EventCategory]struct{}, 3)
	mockBERepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
			assert.Equal(t, model.ChainBase, be.Chain)
			assert.Equal(t, model.NetworkSepolia, be.Network)
			assert.Equal(t, txHash, be.TxHash)
			if assert.NotNil(t, be.WatchedAddress) {
				assert.Equal(t, watchedAddress, *be.WatchedAddress)
			}
			assert.True(t, strings.HasPrefix(be.Delta, "-"))
			assert.NotEmpty(t, be.EventID)
			ingestedCategories[be.EventCategory] = struct{}{}
			return true, nil
		}).
		Times(3)

	mockBalanceRepo.EXPECT().
		AdjustBalanceTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, watchedAddress, tokenID, &walletID, &orgID, gomock.Any(), cursorSequence, txHash).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, _ uuid.UUID, _ *string, _ *string, delta string, _ int64, _ string) error {
			assert.True(t, strings.HasPrefix(delta, "-"))
			return nil
		}).
		Times(3)

	mockCursorRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, watchedAddress, gomock.Any(), cursorSequence, int64(1)).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, cursorValue *string, _ int64, _ int64) error {
			if assert.NotNil(t, cursorValue) {
				assert.Equal(t, txHash, *cursorValue)
			}
			return nil
		}).
		Times(1)

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, cursorSequence).
		Return(nil).
		Times(1)

	ingestInputCh := make(chan event.NormalizedBatch, 1)
	ing := ingester.New(
		mockDB,
		mockTxRepo,
		mockBERepo,
		mockBalanceRepo,
		mockTokenRepo,
		mockCursorRepo,
		mockConfigRepo,
		ingestInputCh,
		slog.Default(),
	)

	ingestInputCh <- normalized
	close(ingestInputCh)

	require.NoError(t, ing.Run(context.Background()))
	assert.Len(t, ingestedCategories, 3)
	assert.Contains(t, ingestedCategories, model.EventCategoryTransfer)
	assert.Contains(t, ingestedCategories, model.EventCategoryFeeExecutionL2)
	assert.Contains(t, ingestedCategories, model.EventCategoryFeeDataL1)
}
