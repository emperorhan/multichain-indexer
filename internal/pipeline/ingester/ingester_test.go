package ingester

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/retry"
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
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockBalanceRepo.EXPECT().
		GetAmountTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		AnyTimes().
		Return("0", nil)
	return ctrl,
		storemocks.NewMockTxBeginner(ctrl),
		storemocks.NewMockTransactionRepository(ctrl),
		storemocks.NewMockBalanceEventRepository(ctrl),
		mockBalanceRepo,
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
	Requested        []int64
	Applied          []int64
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
	basePrev := "ABCDEF"
	baseNew := "0xabcdef"
	assert.False(t, isCanonicalityDrift(event.NormalizedBatch{
		Chain:                  model.ChainBase,
		PreviousCursorValue:    &basePrev,
		NewCursorValue:         &baseNew,
		PreviousCursorSequence: 10,
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
		Chain:             model.ChainSolana,
		Network:           model.NetworkDevnet,
		Address:           "addr1",
		NewCursorValue:    &seedCursor,
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
		PreviousCursorSequence: seedCursorSeq,
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
		PreviousCursorSequence: rewindSigSeq,
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

func TestProcessBatch_BaseAliasCanonicalizesTxHashAndCursor(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
	_ = ctrl

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()
	tokenID := uuid.New()
	aliasTxHash := "ABCDEF"
	canonicalTxHash := "0xabcdef"
	cursorAlias := "ABCDEF"
	canonicalCursor := "0xabcdef"

	batch := event.NormalizedBatch{
		Chain:   model.ChainBase,
		Network: model.NetworkSepolia,
		Address: "0x1111111111111111111111111111111111111111",
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      aliasTxHash,
				BlockCursor: 150,
				FeeAmount:   "0",
				FeePayer:    "0x1111111111111111111111111111111111111111",
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						EventCategory:         model.EventCategoryTransfer,
						EventAction:           "transfer",
						ProgramID:             "0xbase-program",
						ContractAddress:       "ETH",
						Address:               "0x1111111111111111111111111111111111111111",
						CounterpartyAddress:   "0x2222222222222222222222222222222222222222",
						Delta:                 "-1",
						EventID:               "event-base-alias",
						TokenType:             model.TokenTypeNative,
					},
				},
			},
		},
		NewCursorValue:    &cursorAlias,
		NewCursorSequence: 150,
	}

	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tx *model.Transaction) (uuid.UUID, error) {
			assert.Equal(t, canonicalTxHash, tx.TxHash)
			return txID, nil
		})

	mockTokenRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(tokenID, nil)

	mockBERepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
			assert.Equal(t, canonicalTxHash, be.TxHash)
			return true, nil
		})

	mockBalanceRepo.EXPECT().
		AdjustBalanceTx(
			gomock.Any(), gomock.Any(),
			model.ChainBase, model.NetworkSepolia, "0x1111111111111111111111111111111111111111",
			tokenID, nil, nil,
			"-1", int64(150), canonicalTxHash,
		).
		Return(nil)

	mockCursorRepo.EXPECT().
		UpsertTx(
			gomock.Any(), gomock.Any(),
			model.ChainBase, model.NetworkSepolia, "0x1111111111111111111111111111111111111111",
			&canonicalCursor, int64(150), int64(1),
		).
		Return(nil)

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, int64(150)).
		Return(nil)

	require.NoError(t, ing.processBatch(context.Background(), batch))
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
			if assert.NotNil(t, be.BalanceBefore) {
				assert.Equal(t, "0", *be.BalanceBefore)
			}
			if assert.NotNil(t, be.BalanceAfter) {
				assert.Equal(t, "1000000", *be.BalanceAfter)
			}
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
			if assert.NotNil(t, be.BalanceBefore) {
				assert.Equal(t, "0", *be.BalanceBefore)
			}
			if assert.NotNil(t, be.BalanceAfter) {
				assert.Equal(t, "-2000000", *be.BalanceAfter)
			}
			return true, nil
		})
	mockBERepo.EXPECT().UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
			assert.Equal(t, "-5000", be.Delta)
			assert.Equal(t, model.EventCategoryFee, be.EventCategory)
			if assert.NotNil(t, be.BalanceBefore) {
				assert.Equal(t, "0", *be.BalanceBefore)
			}
			if assert.NotNil(t, be.BalanceAfter) {
				assert.Equal(t, "-5000", *be.BalanceAfter)
			}
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

func TestProcessBatch_BaseReplay_SecondPassSkipsBalanceAdjust(t *testing.T) {
	ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
	_ = ctrl

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()
	tokenID := uuid.New()
	cursorVal := "0xbase_tx_1"

	batch := event.NormalizedBatch{
		Chain:   model.ChainBase,
		Network: model.NetworkSepolia,
		Address: "0x1111111111111111111111111111111111111111",
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      "0xbase_tx_1",
				BlockCursor: 200,
				FeeAmount:   "21000000030000",
				FeePayer:    "0x1111111111111111111111111111111111111111",
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{
					{
						OuterInstructionIndex: 7,
						InnerInstructionIndex: -1,
						EventCategory:         model.EventCategoryTransfer,
						EventAction:           "native_transfer",
						ContractAddress:       "ETH",
						Address:               "0x1111111111111111111111111111111111111111",
						CounterpartyAddress:   "0x2222222222222222222222222222222222222222",
						Delta:                 "-100000000000000000",
						EventID:               "base|sepolia|0xbase_tx_1|tx:log:7|addr:0x1111111111111111111111111111111111111111|asset:ETH|cat:TRANSFER",
						TokenType:             model.TokenTypeNative,
					},
					{
						OuterInstructionIndex: 7,
						InnerInstructionIndex: -1,
						EventCategory:         model.EventCategoryFeeExecutionL2,
						EventAction:           "fee_execution_l2",
						ContractAddress:       "ETH",
						Address:               "0x1111111111111111111111111111111111111111",
						Delta:                 "-21000000000000",
						EventID:               "base|sepolia|0xbase_tx_1|tx:log:7|addr:0x1111111111111111111111111111111111111111|asset:ETH|cat:FEE_EXECUTION_L2",
						TokenType:             model.TokenTypeNative,
					},
					{
						OuterInstructionIndex: 7,
						InnerInstructionIndex: -1,
						EventCategory:         model.EventCategoryFeeDataL1,
						EventAction:           "fee_data_l1",
						ContractAddress:       "ETH",
						Address:               "0x1111111111111111111111111111111111111111",
						Delta:                 "-30000",
						EventID:               "base|sepolia|0xbase_tx_1|tx:log:7|addr:0x1111111111111111111111111111111111111111|asset:ETH|cat:FEE_DATA_L1",
						TokenType:             model.TokenTypeNative,
					},
				},
			},
		},
		NewCursorValue:    &cursorVal,
		NewCursorSequence: 200,
	}

	setupBeginTx(mockDB)
	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(txID, nil).
		Times(2)

	mockTokenRepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(tokenID, nil).
		Times(6)

	upsertEventCalls := 0
	mockBERepo.EXPECT().
		UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
			upsertEventCalls++
			assert.Equal(t, model.ChainBase, be.Chain)
			assert.Equal(t, model.NetworkSepolia, be.Network)
			assert.Equal(t, "0xbase_tx_1", be.TxHash)
			return upsertEventCalls <= 3, nil
		}).
		Times(6)

	mockBalanceRepo.EXPECT().
		AdjustBalanceTx(
			gomock.Any(),
			gomock.Any(),
			model.ChainBase,
			model.NetworkSepolia,
			"0x1111111111111111111111111111111111111111",
			tokenID,
			nil,
			nil,
			gomock.Any(),
			int64(200),
			"0xbase_tx_1",
		).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, _ uuid.UUID, _ *string, _ *string, delta string, _ int64, _ string) error {
			assert.True(t, strings.HasPrefix(delta, "-"))
			return nil
		}).
		Times(3)

	mockCursorRepo.EXPECT().
		UpsertTx(
			gomock.Any(),
			gomock.Any(),
			model.ChainBase,
			model.NetworkSepolia,
			"0x1111111111111111111111111111111111111111",
			&cursorVal,
			int64(200),
			int64(1),
		).
		Return(nil).
		Times(2)

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, int64(200)).
		Return(nil).
		Times(2)

	require.NoError(t, ing.processBatch(context.Background(), batch))
	require.NoError(t, ing.processBatch(context.Background(), batch))
}

func TestIngester_Run_TransientPreCommitFailureRetriesDeterministically(t *testing.T) {
	testCases := []struct {
		name            string
		chain           model.Chain
		network         model.Network
		address         string
		txHash          string
		blockCursor     int64
		cursorValue     string
		contractAddress string
		programID       string
		counterparty    string
		delta           string
		eventID         string
	}{
		{
			name:            "solana-devnet",
			chain:           model.ChainSolana,
			network:         model.NetworkDevnet,
			address:         "addr-retry-solana",
			txHash:          "sol-retry-tx-1",
			blockCursor:     111,
			cursorValue:     "sol-retry-tx-1",
			contractAddress: "11111111111111111111111111111111",
			programID:       "11111111111111111111111111111111",
			counterparty:    "counterparty-sol",
			delta:           "1000",
			eventID:         "solana|devnet|sol-retry-tx-1|tx:outer:0:inner:-1|addr:addr-retry-solana|asset:11111111111111111111111111111111|cat:TRANSFER",
		},
		{
			name:            "base-sepolia",
			chain:           model.ChainBase,
			network:         model.NetworkSepolia,
			address:         "0x1111111111111111111111111111111111111111",
			txHash:          "0xbase_retry_tx_1",
			blockCursor:     222,
			cursorValue:     "0xbase_retry_tx_1",
			contractAddress: "ETH",
			programID:       "0xbase-program",
			counterparty:    "0x2222222222222222222222222222222222222222",
			delta:           "-1000",
			eventID:         "base|sepolia|0xbase_retry_tx_1|tx:log:7|addr:0x1111111111111111111111111111111111111111|asset:ETH|cat:TRANSFER",
		},
	}

	type canonicalTuple struct {
		EventID  string
		TxHash   string
		Address  string
		Category model.EventCategory
		Delta    string
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
			_ = ctrl

			normalizedCh := make(chan event.NormalizedBatch, 1)
			ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())
			ing.retryMaxAttempts = 2
			ing.retryDelayStart = 0
			ing.retryDelayMax = 0
			ing.sleepFn = func(context.Context, time.Duration) error { return nil }

			txID := uuid.New()
			tokenID := uuid.New()

			batch := event.NormalizedBatch{
				Chain:   tc.chain,
				Network: tc.network,
				Address: tc.address,
				Transactions: []event.NormalizedTransaction{
					{
						TxHash:      tc.txHash,
						BlockCursor: tc.blockCursor,
						FeeAmount:   "0",
						FeePayer:    tc.address,
						Status:      model.TxStatusSuccess,
						ChainData:   json.RawMessage("{}"),
						BalanceEvents: []event.NormalizedBalanceEvent{
							{
								OuterInstructionIndex: 0,
								InnerInstructionIndex: -1,
								EventCategory:         model.EventCategoryTransfer,
								EventAction:           "transfer",
								ProgramID:             tc.programID,
								ContractAddress:       tc.contractAddress,
								Address:               tc.address,
								CounterpartyAddress:   tc.counterparty,
								Delta:                 tc.delta,
								EventID:               tc.eventID,
								TokenType:             model.TokenTypeNative,
							},
						},
					},
				},
				NewCursorValue:    &tc.cursorValue,
				NewCursorSequence: tc.blockCursor,
			}

			setupBeginTx(mockDB)
			setupBeginTx(mockDB)

			mockTxRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, tx *model.Transaction) (uuid.UUID, error) {
					assert.Equal(t, tc.chain, tx.Chain)
					assert.Equal(t, tc.network, tx.Network)
					assert.Equal(t, tc.txHash, tx.TxHash)
					return txID, nil
				}).
				Times(2)

			mockTokenRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, token *model.Token) (uuid.UUID, error) {
					assert.Equal(t, tc.chain, token.Chain)
					assert.Equal(t, tc.network, token.Network)
					assert.Equal(t, tc.contractAddress, token.ContractAddress)
					return tokenID, nil
				}).
				Times(2)

			tuples := make([]canonicalTuple, 0, 2)
			mockBERepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
					tuples = append(tuples, canonicalTuple{
						EventID:  be.EventID,
						TxHash:   be.TxHash,
						Address:  be.Address,
						Category: be.EventCategory,
						Delta:    be.Delta,
					})
					return true, nil
				}).
				Times(2)

			mockBalanceRepo.EXPECT().
				AdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					tokenID, nil, nil,
					tc.delta, tc.blockCursor, tc.txHash,
				).
				Return(nil).
				Times(2)

			cursorAttempts := 0
			mockCursorRepo.EXPECT().
				UpsertTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					&tc.cursorValue, tc.blockCursor, int64(1),
				).
				DoAndReturn(func(context.Context, *sql.Tx, model.Chain, model.Network, string, *string, int64, int64) error {
					cursorAttempts++
					if cursorAttempts == 1 {
						return retry.Transient(errors.New("transient cursor write timeout"))
					}
					return nil
				}).
				Times(2)

			mockConfigRepo.EXPECT().
				UpdateWatermarkTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, tc.blockCursor).
				Return(nil).
				Times(1)

			normalizedCh <- batch
			close(normalizedCh)

			require.NoError(t, ing.Run(context.Background()))
			require.Len(t, tuples, 2)
			assert.Equal(t, tuples[0], tuples[1], "canonical tuple changed between fail-first and retry attempts")
			assert.Equal(t, 2, cursorAttempts)
		})
	}
}

func TestIngester_ProcessBatchWithRetry_TerminalFailure_NoRetryAcrossMandatoryChains(t *testing.T) {
	testCases := []struct {
		name      string
		chain     model.Chain
		network   model.Network
		address   string
		txHash    string
		txCursor  int64
		txStatus  model.TxStatus
		feePayer  string
		feeAmount string
	}{
		{
			name:      "solana-devnet",
			chain:     model.ChainSolana,
			network:   model.NetworkDevnet,
			address:   "addr-sol-terminal",
			txHash:    "sig-terminal-1",
			txCursor:  101,
			txStatus:  model.TxStatusSuccess,
			feePayer:  "addr-sol-terminal",
			feeAmount: "0",
		},
		{
			name:      "base-sepolia",
			chain:     model.ChainBase,
			network:   model.NetworkSepolia,
			address:   "0x1111111111111111111111111111111111111111",
			txHash:    "0xbase-terminal-1",
			txCursor:  202,
			txStatus:  model.TxStatusSuccess,
			feePayer:  "0x1111111111111111111111111111111111111111",
			feeAmount: "0",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)

			normalizedCh := make(chan event.NormalizedBatch, 1)
			ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())
			ing.retryMaxAttempts = 3
			ing.retryDelayStart = time.Millisecond
			ing.retryDelayMax = 2 * time.Millisecond
			sleepCalls := 0
			ing.sleepFn = func(context.Context, time.Duration) error {
				sleepCalls++
				return nil
			}

			batch := event.NormalizedBatch{
				Chain:   tc.chain,
				Network: tc.network,
				Address: tc.address,
				Transactions: []event.NormalizedTransaction{
					{
						TxHash:      tc.txHash,
						BlockCursor: tc.txCursor,
						FeeAmount:   tc.feeAmount,
						FeePayer:    tc.feePayer,
						Status:      tc.txStatus,
						ChainData:   json.RawMessage("{}"),
					},
				},
				NewCursorSequence: tc.txCursor,
			}

			setupBeginTx(mockDB)
			mockTxRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(uuid.Nil, retry.Terminal(errors.New("constraint violation"))).
				Times(1)

			err := ing.processBatchWithRetry(context.Background(), batch)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "terminal_failure stage=ingester.process_batch")
			assert.Equal(t, 0, sleepCalls)
		})
	}
}

func TestIngester_ProcessBatchWithRetry_TransientExhaustion_StageDiagnostic(t *testing.T) {
	_, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())
	ing.retryMaxAttempts = 2
	ing.retryDelayStart = 0
	ing.retryDelayMax = 0
	ing.sleepFn = func(context.Context, time.Duration) error { return nil }

	mockDB.EXPECT().
		BeginTx(gomock.Any(), gomock.Nil()).
		Return(nil, retry.Transient(errors.New("db timeout"))).
		Times(2)

	err := ing.processBatchWithRetry(context.Background(), event.NormalizedBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transient_recovery_exhausted stage=ingester.process_batch")
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

func TestAddDecimalStrings(t *testing.T) {
	tests := []struct {
		name    string
		left    string
		right   string
		want    string
		wantErr bool
	}{
		{name: "positive and negative", left: "100", right: "-40", want: "60"},
		{name: "negative and negative", left: "-10", right: "-5", want: "-15"},
		{name: "zero", left: "0", right: "0", want: "0"},
		{name: "invalid", left: "abc", right: "1", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := addDecimalStrings(tc.left, tc.right)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
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

func TestIngester_Run_PanicsOnProcessBatchError(t *testing.T) {
	_, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())
	ing.retryMaxAttempts = 1

	normalizedCh <- event.NormalizedBatch{
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
	close(normalizedCh)

	mockDB.EXPECT().BeginTx(gomock.Any(), gomock.Nil()).
		Return(nil, errors.New("db down"))

	require.Panics(t, func() {
		_ = ing.Run(context.Background())
	})
}
