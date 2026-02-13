package ingester

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// fakeDriver / fakeConn / fakeTxImpl provide a minimal sql.Driver
// so we can call BeginTx and get a real *sql.Tx for testing.
type fakeDriver struct{}
type fakeConn struct{}
type fakeTxImpl struct{}

func (d *fakeDriver) Open(name string) (driver.Conn, error) { return &fakeConn{}, nil }
func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}
func (c *fakeConn) Close() error                     { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)        { return &fakeTxImpl{}, nil }
func (tx *fakeTxImpl) Commit() error                  { return nil }
func (tx *fakeTxImpl) Rollback() error                { return nil }

func init() {
	sql.Register("fake_ingester", &fakeDriver{})
}

func openFakeDB() *sql.DB {
	db, _ := sql.Open("fake_ingester", "")
	return db
}

func newIngesterMocks(t *testing.T) (
	*gomock.Controller,
	*storemocks.MockTxBeginner,
	*storemocks.MockTransactionRepository,
	*storemocks.MockTransferRepository,
	*storemocks.MockBalanceRepository,
	*storemocks.MockTokenRepository,
	*storemocks.MockCursorRepository,
	*storemocks.MockIndexerConfigRepository,
) {
	ctrl := gomock.NewController(t)
	return ctrl,
		storemocks.NewMockTxBeginner(ctrl),
		storemocks.NewMockTransactionRepository(ctrl),
		storemocks.NewMockTransferRepository(ctrl),
		storemocks.NewMockBalanceRepository(ctrl),
		storemocks.NewMockTokenRepository(ctrl),
		storemocks.NewMockCursorRepository(ctrl),
		storemocks.NewMockIndexerConfigRepository(ctrl)
}

func setupBeginTx(mockDB *storemocks.MockTxBeginner) {
	fakeDB := openFakeDB()
	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		DoAndReturn(func(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
			return fakeDB.BeginTx(ctx, opts)
		})
}

func TestProcessBatch_Deposit(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
	_ = ctrl

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()
	tokenID := uuid.New()
	bt := time.Now()
	cursorVal := "sig1"
	walletID := "wallet-1"
	orgID := "org-1"

	batch := event.NormalizedBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		WalletID: &walletID,
		OrgID:   &orgID,
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      "sig1",
				BlockCursor: 100,
				BlockTime:   &bt,
				FeeAmount:   "5000",
				FeePayer:    "otherAddr",
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
				Transfers: []event.NormalizedTransfer{
					{
						InstructionIndex: 0,
						ContractAddress:  "11111111111111111111111111111111",
						FromAddress:      "otherAddr",
						ToAddress:        "addr1", // deposit
						Amount:           "1000000",
						TokenSymbol:      "SOL",
						TokenName:        "Solana",
						TokenDecimals:    9,
						TokenType:        model.TokenTypeNative,
					},
				},
			},
		},
		NewCursorValue:    &cursorVal,
		NewCursorSequence: 100,
	}

	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(txID, nil)

	mockTokenRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(tokenID, nil)

	mockTransferRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tf *model.Transfer) error {
			assert.Equal(t, model.ChainSolana, tf.Chain)
			assert.Equal(t, "otherAddr", tf.FromAddress)
			assert.Equal(t, "addr1", tf.ToAddress)
			require.NotNil(t, tf.Direction)
			assert.Equal(t, model.DirectionDeposit, *tf.Direction)
			return nil
		})

	// Positive delta for deposit
	mockBalanceRepo.EXPECT().AdjustBalanceTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, "addr1",
		tokenID, &walletID, &orgID,
		"1000000", int64(100), "sig1",
	).Return(nil)

	// No fee deduction (feePayer != watched address)

	mockCursorRepo.EXPECT().UpsertTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, "addr1",
		&cursorVal, int64(100), int64(1),
	).Return(nil)

	mockConfigRepo.EXPECT().UpdateWatermarkTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, int64(100),
	).Return(nil)

	err := ing.processBatch(context.Background(), batch)
	require.NoError(t, err)
}

func TestProcessBatch_Withdrawal_WithFee(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
	_ = ctrl

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()
	tokenID := uuid.New()
	nativeTokenID := uuid.New()
	cursorVal := "sig1"
	walletID := "wallet-1"
	orgID := "org-1"

	batch := event.NormalizedBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		WalletID: &walletID,
		OrgID:   &orgID,
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      "sig1",
				BlockCursor: 100,
				FeeAmount:   "5000",
				FeePayer:    "addr1", // watched address is fee payer
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
				Transfers: []event.NormalizedTransfer{
					{
						InstructionIndex: 0,
						ContractAddress:  "11111111111111111111111111111111",
						FromAddress:      "addr1", // withdrawal
						ToAddress:        "otherAddr",
						Amount:           "2000000",
						TokenType:        model.TokenTypeNative,
					},
				},
			},
		},
		NewCursorValue:    &cursorVal,
		NewCursorSequence: 100,
	}

	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(txID, nil)

	// First token upsert: for the transfer
	mockTokenRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(tokenID, nil)

	mockTransferRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tf *model.Transfer) error {
			require.NotNil(t, tf.Direction)
			assert.Equal(t, model.DirectionWithdrawal, *tf.Direction)
			return nil
		})

	// Negated amount for withdrawal
	mockBalanceRepo.EXPECT().AdjustBalanceTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, "addr1",
		tokenID, &walletID, &orgID,
		"-2000000", int64(100), "sig1",
	).Return(nil)

	// Fee deduction: native token upsert
	mockTokenRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nativeTokenID, nil)

	// Fee deduction: negative fee balance
	mockBalanceRepo.EXPECT().AdjustBalanceTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, "addr1",
		nativeTokenID, &walletID, &orgID,
		"-5000", int64(100), "sig1",
	).Return(nil)

	mockCursorRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	mockConfigRepo.EXPECT().UpdateWatermarkTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	err := ing.processBatch(context.Background(), batch)
	require.NoError(t, err)
}

func TestProcessBatch_NoDirection(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockTransferRepo, _, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	ing := New(mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()
	tokenID := uuid.New()

	batch := event.NormalizedBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      "sig1",
				BlockCursor: 100,
				FeeAmount:   "0",
				FeePayer:    "otherAddr",
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
				Transfers: []event.NormalizedTransfer{
					{
						InstructionIndex: 0,
						ContractAddress:  "mint1",
						FromAddress:      "neither1",
						ToAddress:        "neither2", // not watched
						Amount:           "1000",
						TokenType:        model.TokenTypeFungible,
					},
				},
			},
		},
		NewCursorSequence: 100,
	}

	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(txID, nil)

	mockTokenRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(tokenID, nil)

	mockTransferRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tf *model.Transfer) error {
			assert.Nil(t, tf.Direction)
			return nil
		})

	// No AdjustBalanceTx call (direction is nil)
	// No fee deduction (fee is "0")
	// No cursor update (NewCursorValue is nil)

	mockConfigRepo.EXPECT().UpdateWatermarkTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	err := ing.processBatch(context.Background(), batch)
	require.NoError(t, err)
}

func TestProcessBatch_NoFeeDeduction_FailedTx(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
	_ = ctrl

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()
	cursorVal := "sig1"

	batch := event.NormalizedBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      "sig1",
				BlockCursor: 100,
				FeeAmount:   "5000",
				FeePayer:    "addr1",              // watched address is fee payer
				Status:      model.TxStatusFailed, // BUT tx is failed
				ChainData:   json.RawMessage("{}"),
			},
		},
		NewCursorValue:    &cursorVal,
		NewCursorSequence: 100,
	}

	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(txID, nil)

	// No fee deduction for failed tx
	// No token/transfer/balance calls

	mockCursorRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	mockConfigRepo.EXPECT().UpdateWatermarkTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	err := ing.processBatch(context.Background(), batch)
	require.NoError(t, err)
}

func TestProcessBatch_BeginTxError(t *testing.T) {
	_, mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	batch := event.NormalizedBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
	}

	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		Return(nil, errors.New("db down"))

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin tx")
}

func TestProcessBatch_UpsertTxError(t *testing.T) {
	_, mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	batch := event.NormalizedBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:    "sig1",
				FeeAmount: "0",
				FeePayer:  "other",
				Status:    model.TxStatusSuccess,
				ChainData: json.RawMessage("{}"),
			},
		},
	}

	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(uuid.Nil, errors.New("constraint violation"))

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert tx")
}

func TestIngester_Run_ContextCancel(t *testing.T) {
	_, mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(mockDB, mockTxRepo, mockTransferRepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ing.Run(ctx)
	assert.Equal(t, context.Canceled, err)
}
