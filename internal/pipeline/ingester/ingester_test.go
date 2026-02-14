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

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	"github.com/google/uuid"
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
func (c *fakeConn) Close() error              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return &fakeTxImpl{}, nil }
func (tx *fakeTxImpl) Commit() error          { return nil }
func (tx *fakeTxImpl) Rollback() error        { return nil }

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
	*storemocks.MockBalanceEventRepository,
	*storemocks.MockBalanceRepository,
	*storemocks.MockTokenRepository,
	*storemocks.MockCursorRepository,
	*storemocks.MockIndexerConfigRepository,
) {
	ctrl := gomock.NewController(t)
	return ctrl,
		storemocks.NewMockTxBeginner(ctrl),
		storemocks.NewMockTransactionRepository(ctrl),
		storemocks.NewMockBalanceEventRepository(ctrl),
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

type testCursorStateRepo struct {
	LastValue    *string
	LastSequence int64
	Upserts      []int64
}

func (r *testCursorStateRepo) Get(_ context.Context, _ model.Chain, _ model.Network, _ string) (*model.AddressCursor, error) {
	return nil, nil
}

func (r *testCursorStateRepo) UpsertTx(
	_ context.Context,
	_ *sql.Tx,
	_ model.Chain,
	_ model.Network,
	_ string,
	cursorValue *string,
	cursorSequence int64,
	_ int64,
) error {
	if cursorValue != nil {
		v := *cursorValue
		r.LastValue = &v
	}
	r.LastSequence = cursorSequence
	r.Upserts = append(r.Upserts, cursorSequence)
	return nil
}

func (r *testCursorStateRepo) EnsureExists(_ context.Context, _ model.Chain, _ model.Network, _ string) error {
	return nil
}

type testIndexerConfigStateRepo struct {
	HighestWatermark int64
	Requested       []int64
	Applied        []int64
}

func (r *testIndexerConfigStateRepo) Get(_ context.Context, _ model.Chain, _ model.Network) (*model.IndexerConfig, error) {
	return nil, nil
}

func (r *testIndexerConfigStateRepo) Upsert(_ context.Context, _ *model.IndexerConfig) error {
	return nil
}

func (r *testIndexerConfigStateRepo) UpdateWatermarkTx(
	_ context.Context,
	_ *sql.Tx,
	_ model.Chain,
	_ model.Network,
	ingestedSequence int64,
) error {
	r.Requested = append(r.Requested, ingestedSequence)
	if ingestedSequence > r.HighestWatermark {
		r.HighestWatermark = ingestedSequence
	}
	r.Applied = append(r.Applied, r.HighestWatermark)
	return nil
}

func Test_isCanonicalityDrift(t *testing.T) {
	oldSig := "old_sig"
	newSig := "new_sig"

	assert.False(t, isCanonicalityDrift(event.NormalizedBatch{
		PreviousCursorValue:    nil,
		NewCursorValue:         &newSig,
		PreviousCursorSequence: 10,
		NewCursorSequence:      10,
	}))
	assert.False(t, isCanonicalityDrift(event.NormalizedBatch{
		PreviousCursorValue:    &oldSig,
		NewCursorValue:         &newSig,
		PreviousCursorSequence: 0,
		NewCursorSequence:      10,
	}))
	assert.True(t, isCanonicalityDrift(event.NormalizedBatch{
		PreviousCursorValue:    &oldSig,
		NewCursorValue:         &newSig,
		PreviousCursorSequence: 10,
		NewCursorSequence:      10,
	}))
	assert.True(t, isCanonicalityDrift(event.NormalizedBatch{
		PreviousCursorValue:    &oldSig,
		NewCursorValue:         &oldSig,
		PreviousCursorSequence: 20,
		NewCursorSequence:      10,
	}))
}

func TestProcessBatch_ReorgDrift_TriggersDeterministicRollbackPath(t *testing.T) {
	ctrl, mockDB, _, _, _, _, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
	_ = ctrl

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, nil, nil, nil, nil, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())
	// Keep SQL path unchanged; replace only rollback path for deterministic simulation.
	rollbackCalls := 0
	ing.reorgHandler = func(ctx context.Context, tx *sql.Tx, got event.NormalizedBatch) error {
		rollbackCalls++
		assert.Equal(t, "addr1", got.Address)
		assert.NotNil(t, got.PreviousCursorValue)
		assert.Equal(t, "old_sig", *got.PreviousCursorValue)
		assert.Equal(t, int64(100), got.PreviousCursorSequence)
		return nil
	}

	prevSig := "old_sig"
	newSig := "new_sig"
	batch := event.NormalizedBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		Address:                "addr1",
		PreviousCursorValue:    &prevSig,
		PreviousCursorSequence: 100,
		NewCursorValue:         &newSig,
		NewCursorSequence:      100,
	}

	setupBeginTx(mockDB)
	setupBeginTx(mockDB)
	err := ing.processBatch(context.Background(), batch)
	require.NoError(t, err)

	err = ing.processBatch(context.Background(), batch)
	require.NoError(t, err)
	assert.Equal(t, 2, rollbackCalls)
}

func TestProcessBatch_PostRecoveryCursorAndWatermarkMonotonicity(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := storemocks.NewMockTxBeginner(ctrl)
	mockTxRepo := storemocks.NewMockTransactionRepository(ctrl)
	mockBERepo := storemocks.NewMockBalanceEventRepository(ctrl)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockTokenRepo := storemocks.NewMockTokenRepository(ctrl)

	cursorRepo := &testCursorStateRepo{}
	configRepo := &testIndexerConfigStateRepo{}
	// Seed a high-watermark state to emulate previously observed progress.
	configRepo.HighestWatermark = 300

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, cursorRepo, configRepo, normalizedCh, slog.Default())

	seedCursor := "seed_sig"
	seedCursorSeq := int64(300)
	seedBatch := event.NormalizedBatch{
		Chain:           model.ChainSolana,
		Network:         model.NetworkDevnet,
		Address:         "addr1",
		NewCursorValue:  &seedCursor,
		NewCursorSequence: seedCursorSeq,
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      "seed_tx",
				BlockCursor: 300,
				FeeAmount:   "0",
				FeePayer:    "other",
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
			},
		},
	}
	seedTxID := uuid.New()
	setupBeginTx(mockDB)
	mockTxRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(seedTxID, nil)

	require.NoError(t, ing.processBatch(context.Background(), seedBatch))
	assert.Equal(t, int64(300), cursorRepo.LastSequence)
	assert.Equal(t, []int64{300}, cursorRepo.Upserts)
	assert.Equal(t, int64(300), configRepo.HighestWatermark)
	assert.Equal(t, []int64{300}, configRepo.Requested)
	assert.Equal(t, []int64{300}, configRepo.Applied)

	// Recovery rollback should not regress watermark sequence (GREATEST semantics),
	// while cursor is allowed to rewind for replay.
	rewindSig := "rewind_sig"
	rewindSigSeq := int64(100)
	ing.reorgHandler = func(ctx context.Context, tx *sql.Tx, got event.NormalizedBatch) error {
		if err := configRepo.UpdateWatermarkTx(ctx, tx, got.Chain, got.Network, rewindSigSeq); err != nil {
			return err
		}
		return cursorRepo.UpsertTx(ctx, tx, got.Chain, got.Network, got.Address, &rewindSig, rewindSigSeq, 0)
	}

	newSig := "post_recovery_sig"
	recoveryBatch := event.NormalizedBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		Address:                "addr1",
		PreviousCursorValue:    &seedCursor,
		PreviousCursorSequence:  seedCursorSeq,
		NewCursorValue:         &newSig,
		NewCursorSequence:      seedCursorSeq,
	}
	setupBeginTx(mockDB)
	require.NoError(t, ing.processBatch(context.Background(), recoveryBatch))
	assert.Equal(t, int64(100), cursorRepo.LastSequence)
	assert.Equal(t, int64(300), configRepo.HighestWatermark)
	assert.Equal(t, []int64{300, 100}, cursorRepo.Upserts)
	assert.Equal(t, []int64{300, 100}, configRepo.Requested)
	assert.Equal(t, []int64{300, 300}, configRepo.Applied)

	replaySig := "replay_sig"
	replayBatch := event.NormalizedBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		Address:                "addr1",
		PreviousCursorValue:    &rewindSig,
		PreviousCursorSequence:  rewindSigSeq,
		NewCursorValue:         &replaySig,
		NewCursorSequence:      seedCursorSeq,
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      "seed_tx",
				BlockCursor: 300,
				FeeAmount:   "0",
				FeePayer:    "other",
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
			},
		},
	}
	setupBeginTx(mockDB)
	mockTxRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(seedTxID, nil)

	require.NoError(t, ing.processBatch(context.Background(), replayBatch))
	assert.Equal(t, int64(300), cursorRepo.LastSequence)
	assert.Equal(t, []int64{300, 100, 300}, cursorRepo.Upserts)
	assert.Equal(t, int64(300), configRepo.HighestWatermark)
	assert.Equal(t, []int64{300, 100, 300}, configRepo.Requested)
	assert.Equal(t, []int64{300, 300, 300}, configRepo.Applied)
	assert.Equal(t, int64(300), configRepo.Applied[len(configRepo.Applied)-1])

	// Watermark application must remain monotonic even when a rollback
	// path requests a rewind.
	for i := 1; i < len(configRepo.Applied); i++ {
		assert.GreaterOrEqual(t, configRepo.Applied[i], configRepo.Applied[i-1])
	}

	assert.Greater(t, cursorRepo.Upserts[2], cursorRepo.Upserts[1])
	assert.GreaterOrEqual(t, configRepo.HighestWatermark, cursorRepo.Upserts[2])
}

func TestProcessBatch_Deposit(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
	_ = ctrl

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()
	tokenID := uuid.New()
	bt := time.Now()
	cursorVal := "sig1"
	walletID := "wallet-1"
	orgID := "org-1"

	batch := event.NormalizedBatch{
		Chain:    model.ChainSolana,
		Network:  model.NetworkDevnet,
		Address:  "addr1",
		WalletID: &walletID,
		OrgID:    &orgID,
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      "sig1",
				BlockCursor: 100,
				BlockTime:   &bt,
				FeeAmount:   "5000",
				FeePayer:    "otherAddr",
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						EventCategory:         model.EventCategoryTransfer,
						EventAction:           "system_transfer",
						ProgramID:             "11111111111111111111111111111111",
						ContractAddress:       "11111111111111111111111111111111",
						Address:               "addr1",
						CounterpartyAddress:   "otherAddr",
						Delta:                 "1000000",
						EventID:               "solana|devnet|sig1|tx:outer:0:inner:-1|addr:addr1|asset:11111111111111111111111111111111|cat:TRANSFER",
						TokenSymbol:           "SOL",
						TokenName:             "Solana",
						TokenDecimals:         9,
						TokenType:             model.TokenTypeNative,
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

	mockBERepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
			assert.Equal(t, model.ChainSolana, be.Chain)
			assert.Equal(t, "addr1", be.Address)
			assert.Equal(t, "otherAddr", be.CounterpartyAddress)
			assert.Equal(t, "1000000", be.Delta)
			assert.Equal(t, model.EventCategoryTransfer, be.EventCategory)
			return true, nil
		})

	// Positive delta for deposit
	mockBalanceRepo.EXPECT().AdjustBalanceTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, "addr1",
		tokenID, &walletID, &orgID,
		"1000000", int64(100), "sig1",
	).Return(nil)

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
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
	_ = ctrl

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()
	tokenID := uuid.New()
	feeTokenID := uuid.New()
	cursorVal := "sig1"
	walletID := "wallet-1"
	orgID := "org-1"

	batch := event.NormalizedBatch{
		Chain:    model.ChainSolana,
		Network:  model.NetworkDevnet,
		Address:  "addr1",
		WalletID: &walletID,
		OrgID:    &orgID,
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      "sig1",
				BlockCursor: 100,
				FeeAmount:   "5000",
				FeePayer:    "addr1",
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						EventCategory:         model.EventCategoryTransfer,
						EventAction:           "system_transfer",
						ProgramID:             "11111111111111111111111111111111",
						ContractAddress:       "11111111111111111111111111111111",
						Address:               "addr1",
						CounterpartyAddress:   "otherAddr",
						Delta:                 "-2000000",
						EventID:               "solana|devnet|sig1|tx:outer:0:inner:-1|addr:addr1|asset:11111111111111111111111111111111|cat:TRANSFER",
						TokenType:             model.TokenTypeNative,
					},
					{
						OuterInstructionIndex: -1,
						InnerInstructionIndex: -1,
						EventCategory:         model.EventCategoryFee,
						EventAction:           "transaction_fee",
						ProgramID:             "11111111111111111111111111111111",
						ContractAddress:       "11111111111111111111111111111111",
						Address:               "addr1",
						CounterpartyAddress:   "",
						Delta:                 "-5000",
						EventID:               "solana|devnet|sig1|tx:outer:-1:inner:-1|addr:addr1|asset:11111111111111111111111111111111|cat:FEE",
						TokenSymbol:           "SOL",
						TokenName:             "Solana",
						TokenDecimals:         9,
						TokenType:             model.TokenTypeNative,
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

	// Two token upserts: one for transfer, one for fee
	gomock.InOrder(
		mockTokenRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(tokenID, nil),
		mockTokenRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(feeTokenID, nil),
	)

	// Two balance event upserts
	mockBERepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
			assert.Equal(t, "-2000000", be.Delta)
			return true, nil
		})
	mockBERepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
			assert.Equal(t, "-5000", be.Delta)
			assert.Equal(t, model.EventCategoryFee, be.EventCategory)
			return true, nil
		})

	// Two balance adjustments
	mockBalanceRepo.EXPECT().AdjustBalanceTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, "addr1",
		tokenID, &walletID, &orgID,
		"-2000000", int64(100), "sig1",
	).Return(nil)
	mockBalanceRepo.EXPECT().AdjustBalanceTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, "addr1",
		feeTokenID, &walletID, &orgID,
		"-5000", int64(100), "sig1",
	).Return(nil)

	mockCursorRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	mockConfigRepo.EXPECT().UpdateWatermarkTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	err := ing.processBatch(context.Background(), batch)
	require.NoError(t, err)
}

func TestProcessBatch_NoEvents(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
	_ = ctrl

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()

	batch := event.NormalizedBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:        "sig1",
				BlockCursor:   100,
				FeeAmount:     "0",
				FeePayer:      "otherAddr",
				Status:        model.TxStatusSuccess,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{},
			},
		},
		NewCursorSequence: 100,
	}

	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(txID, nil)

	// No balance event, token, or balance calls

	mockConfigRepo.EXPECT().UpdateWatermarkTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	err := ing.processBatch(context.Background(), batch)
	require.NoError(t, err)
}

func TestProcessBatch_DuplicateEventReplayIsNoop(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
	_ = ctrl

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()
	tokenID := uuid.New()
	cursorVal := "sig1"
	walletID := "wallet-1"
	orgID := "org-1"

	batch := event.NormalizedBatch{
		Chain:    model.ChainSolana,
		Network:  model.NetworkDevnet,
		Address:  "addr1",
		WalletID: &walletID,
		OrgID:    &orgID,
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      "sig1",
				BlockCursor: 100,
				FeeAmount:   "5000",
				FeePayer:    "otherAddr",
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						EventCategory:         model.EventCategoryTransfer,
						EventAction:           "system_transfer",
						ProgramID:             "11111111111111111111111111111111",
						ContractAddress:       "11111111111111111111111111111111",
						Address:               "addr1",
						CounterpartyAddress:   "otherAddr",
						Delta:                 "1000000",
						EventID:               "solana|devnet|sig1|tx:outer:0:inner:-1|addr:addr1|asset:11111111111111111111111111111111|cat:TRANSFER",
						TokenType:             model.TokenTypeNative,
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

	mockBERepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(false, nil)

	mockCursorRepo.EXPECT().UpsertTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, "addr1",
		&cursorVal, int64(100), int64(1),
	).Return(nil)

	mockConfigRepo.EXPECT().UpdateWatermarkTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	err := ing.processBatch(context.Background(), batch)
	require.NoError(t, err)
}

func TestProcessBatch_NoFeeDeduction_FailedTx(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
	_ = ctrl

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()
	cursorVal := "sig1"

	batch := event.NormalizedBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:        "sig1",
				BlockCursor:   100,
				FeeAmount:     "5000",
				FeePayer:      "addr1",
				Status:        model.TxStatusFailed,
				ChainData:     json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{}, // no events for failed tx
			},
		},
		NewCursorValue:    &cursorVal,
		NewCursorSequence: 100,
	}

	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(txID, nil)

	mockCursorRepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	mockConfigRepo.EXPECT().UpdateWatermarkTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	err := ing.processBatch(context.Background(), batch)
	require.NoError(t, err)
}

func TestProcessBatch_BeginTxError(t *testing.T) {
	_, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

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
	_, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

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
	_, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ing.Run(ctx)
	assert.Equal(t, context.Canceled, err)
}
