package ingester

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
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

var (
	fakeCommitErrorMu sync.Mutex
	fakeCommitErrors  []error
)

func (d *fakeDriver) Open(name string) (driver.Conn, error) { return &fakeConn{}, nil }
func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}
func (c *fakeConn) Close() error              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return &fakeTxImpl{}, nil }
func (tx *fakeTxImpl) Commit() error {
	fakeCommitErrorMu.Lock()
	defer fakeCommitErrorMu.Unlock()
	if len(fakeCommitErrors) == 0 {
		return nil
	}
	err := fakeCommitErrors[0]
	fakeCommitErrors = fakeCommitErrors[1:]
	return err
}
func (tx *fakeTxImpl) Rollback() error        { return nil }

func init() {
	sql.Register("fake_ingester", &fakeDriver{})
}

func openFakeDB() *sql.DB {
	db, _ := sql.Open("fake_ingester", "")
	return db
}

func setFakeCommitErrors(errs ...error) {
	fakeCommitErrorMu.Lock()
	defer fakeCommitErrorMu.Unlock()
	fakeCommitErrors = append([]error(nil), errs...)
}

func clearFakeCommitErrors() {
	fakeCommitErrorMu.Lock()
	defer fakeCommitErrorMu.Unlock()
	fakeCommitErrors = nil
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

func TestProcessBatch_DeferredRecoveryReplay_DeduplicatesAndPreservesCursorAcrossMandatoryChains(t *testing.T) {
	testCases := []struct {
		name             string
		chain            model.Chain
		network          model.Network
		address          string
		firstSig         string
		secondSig        string
		thirdSig         string
		contractAddress  string
		programID        string
		counterparty     string
		tokenType        model.TokenType
		previousSequence int64
	}{
		{
			name:             "solana-devnet",
			chain:            model.ChainSolana,
			network:          model.NetworkDevnet,
			address:          "addr-ingester-sidecar-recovery-sol",
			firstSig:         "sig-ingester-recovery-sol-1",
			secondSig:        "sig-ingester-recovery-sol-2",
			thirdSig:         "sig-ingester-recovery-sol-3",
			contractAddress:  "11111111111111111111111111111111",
			programID:        "11111111111111111111111111111111",
			counterparty:     "sol-counterparty",
			tokenType:        model.TokenTypeNative,
			previousSequence: 1000,
		},
		{
			name:             "base-sepolia",
			chain:            model.ChainBase,
			network:          model.NetworkSepolia,
			address:          "0x7777777777777777777777777777777777777777",
			firstSig:         "0xAbCd3401",
			secondSig:        "0xabCD3402",
			thirdSig:         "ABCD3403",
			contractAddress:  "ETH",
			programID:        "0xbase-program",
			counterparty:     "0x8888888888888888888888888888888888888888",
			tokenType:        model.TokenTypeNative,
			previousSequence: 2000,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
			ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, nil, slog.Default())

			firstSeq := tc.previousSequence + 1
			secondSeq := tc.previousSequence + 2
			thirdSeq := tc.previousSequence + 3
			canonicalFirst := canonicalSignatureIdentity(tc.chain, tc.firstSig)
			canonicalSecond := canonicalSignatureIdentity(tc.chain, tc.secondSig)
			canonicalThird := canonicalSignatureIdentity(tc.chain, tc.thirdSig)
			require.NotEmpty(t, canonicalFirst)
			require.NotEmpty(t, canonicalSecond)
			require.NotEmpty(t, canonicalThird)

			eventIDFor := func(txHash string) string {
				return strings.Join([]string{
					string(tc.chain),
					string(tc.network),
					txHash,
					"outer:0|inner:-1",
					tc.address,
					tc.contractAddress,
					string(model.EventCategoryTransfer),
				}, "|")
			}
			firstEventID := eventIDFor(canonicalFirst)
			secondEventID := eventIDFor(canonicalSecond)
			thirdEventID := eventIDFor(canonicalThird)
			expectedEventIDs := map[string]struct{}{
				firstEventID:  {},
				secondEventID: {},
				thirdEventID:  {},
			}

			previousCursor := canonicalSignatureIdentity(tc.chain, "cursor-prev")
			if previousCursor == "" {
				previousCursor = "cursor-prev"
			}
			firstBatch := event.NormalizedBatch{
				Chain:                  tc.chain,
				Network:                tc.network,
				Address:                tc.address,
				PreviousCursorValue:    &previousCursor,
				PreviousCursorSequence: tc.previousSequence,
				NewCursorValue:         &canonicalThird,
				NewCursorSequence:      thirdSeq,
				Transactions: []event.NormalizedTransaction{
					{
						TxHash:      tc.firstSig,
						BlockCursor: firstSeq,
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
								Delta:                 "-1",
								EventID:               firstEventID,
								TokenType:             tc.tokenType,
							},
						},
					},
					{
						TxHash:      tc.thirdSig,
						BlockCursor: thirdSeq,
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
								Delta:                 "-1",
								EventID:               thirdEventID,
								TokenType:             tc.tokenType,
							},
						},
					},
				},
			}

			recoveryBatch := event.NormalizedBatch{
				Chain:                  tc.chain,
				Network:                tc.network,
				Address:                tc.address,
				PreviousCursorValue:    &canonicalThird,
				PreviousCursorSequence: thirdSeq,
				NewCursorValue:         &canonicalThird,
				NewCursorSequence:      thirdSeq,
				Transactions: []event.NormalizedTransaction{
					{
						TxHash:      tc.secondSig,
						BlockCursor: secondSeq,
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
								Delta:                 "-1",
								EventID:               secondEventID,
								TokenType:             tc.tokenType,
							},
						},
					},
					{
						TxHash:      tc.thirdSig,
						BlockCursor: thirdSeq,
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
								Delta:                 "-1",
								EventID:               thirdEventID,
								TokenType:             tc.tokenType,
							},
						},
					},
				},
			}

			setupBeginTx(mockDB)
			setupBeginTx(mockDB)

			txID := uuid.New()
			tokenID := uuid.New()
			mockTxRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(txID, nil).
				Times(4)
			mockTokenRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(tokenID, nil).
				Times(4)

			seenEventIDs := make(map[string]struct{})
			insertedEventIDs := make(map[string]struct{})
			mockBERepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
					if _, exists := seenEventIDs[be.EventID]; exists {
						return false, nil
					}
					seenEventIDs[be.EventID] = struct{}{}
					insertedEventIDs[be.EventID] = struct{}{}
					return true, nil
				}).
				Times(4)

			adjustments := 0
			mockBalanceRepo.EXPECT().
				AdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					tokenID, nil, nil, "-1",
					gomock.Any(), gomock.Any(),
				).
				DoAndReturn(func(context.Context, *sql.Tx, model.Chain, model.Network, string, uuid.UUID, *string, *string, string, int64, string) error {
					adjustments++
					return nil
				}).
				Times(3)

			cursorWrites := make([]int64, 0, 2)
			mockCursorRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, tc.address, &canonicalThird, thirdSeq, gomock.Any()).
				DoAndReturn(func(context.Context, *sql.Tx, model.Chain, model.Network, string, *string, int64, int64) error {
					cursorWrites = append(cursorWrites, thirdSeq)
					return nil
				}).
				Times(2)
			mockConfigRepo.EXPECT().
				UpdateWatermarkTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, thirdSeq).
				Return(nil).
				Times(2)

			require.NoError(t, ing.processBatch(context.Background(), firstBatch))
			require.NoError(t, ing.processBatch(context.Background(), recoveryBatch))

			assert.Equal(t, expectedEventIDs, insertedEventIDs, "deferred recovery replay must reconcile to fully decodable logical event set")
			assert.Equal(t, 3, adjustments, "already-decoded replay events must not double-apply balances")
			require.Len(t, cursorWrites, 2)
			assert.GreaterOrEqual(t, cursorWrites[1], cursorWrites[0], "cursor progression must remain monotonic across deferred-recovery boundary")
		})
	}
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

func TestProcessBatch_MixedFinalityReplayPromotesWithoutDoubleApplyAcrossMandatoryChains(t *testing.T) {
	testCases := []struct {
		name         string
		chain        model.Chain
		network      model.Network
		address      string
		txHash       string
		cursor       int64
		eventID      string
		contractAddr string
		programID    string
		counterparty string
	}{
		{
			name:         "solana-devnet",
			chain:        model.ChainSolana,
			network:      model.NetworkDevnet,
			address:      "addr-finality-sol",
			txHash:       "sig-finality-sol-1",
			cursor:       301,
			eventID:      "solana|devnet|sig-finality-sol-1|tx:outer:0:inner:-1|addr:addr-finality-sol|asset:11111111111111111111111111111111|cat:TRANSFER",
			contractAddr: "11111111111111111111111111111111",
			programID:    "11111111111111111111111111111111",
			counterparty: "addr-counterparty-sol",
		},
		{
			name:         "base-sepolia",
			chain:        model.ChainBase,
			network:      model.NetworkSepolia,
			address:      "0x1111111111111111111111111111111111111111",
			txHash:       "0xbase_finality_1",
			cursor:       302,
			eventID:      "base|sepolia|0xbase_finality_1|tx:log:7|addr:0x1111111111111111111111111111111111111111|asset:ETH|cat:TRANSFER",
			contractAddr: "ETH",
			programID:    "0xbase-program",
			counterparty: "0x2222222222222222222222222222222222222222",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
			_ = ctrl

			normalizedCh := make(chan event.NormalizedBatch, 1)
			ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

			txID := uuid.New()
			tokenID := uuid.New()
			cursorValue := tc.txHash

			buildBatch := func(finality string) event.NormalizedBatch {
				return event.NormalizedBatch{
					Chain:   tc.chain,
					Network: tc.network,
					Address: tc.address,
					Transactions: []event.NormalizedTransaction{
						{
							TxHash:      tc.txHash,
							BlockCursor: tc.cursor,
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
									ContractAddress:       tc.contractAddr,
									Address:               tc.address,
									CounterpartyAddress:   tc.counterparty,
									Delta:                 "-1",
									EventID:               tc.eventID,
									TokenType:             model.TokenTypeNative,
									FinalityState:         finality,
								},
							},
						},
					},
					NewCursorValue:    &cursorValue,
					NewCursorSequence: tc.cursor,
				}
			}

			weaker := buildBatch("confirmed")
			stronger := buildBatch("finalized")

			setupBeginTx(mockDB)
			setupBeginTx(mockDB)

			mockTxRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(txID, nil).
				Times(2)

			mockTokenRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(tokenID, nil).
				Times(2)

			finalityWrites := make([]string, 0, 2)
			call := 0
			mockBERepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
					call++
					finalityWrites = append(finalityWrites, be.FinalityState)
					return call == 1, nil
				}).
				Times(2)

			mockBalanceRepo.EXPECT().
				AdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					tokenID, nil, nil,
					"-1", tc.cursor, tc.txHash,
				).
				Return(nil).
				Times(1)

			mockCursorRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, tc.address, &cursorValue, tc.cursor, int64(1)).
				Return(nil).
				Times(2)

			mockConfigRepo.EXPECT().
				UpdateWatermarkTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, tc.cursor).
				Return(nil).
				Times(2)

			require.NoError(t, ing.processBatch(context.Background(), weaker))
			require.NoError(t, ing.processBatch(context.Background(), stronger))
			assert.Equal(t, []string{"confirmed", "finalized"}, finalityWrites)
		})
	}
}

func TestProcessBatch_RollbackAfterFinalityConvergesWithoutDoubleApplyAcrossMandatoryChains(t *testing.T) {
	testCases := []struct {
		name            string
		chain           model.Chain
		network         model.Network
		address         string
		preForkTxHash   string
		postForkTxHash  string
		preForkEventID  string
		postForkEventID string
		contractAddr    string
		programID       string
		counterparty    string
	}{
		{
			name:            "solana-devnet",
			chain:           model.ChainSolana,
			network:         model.NetworkDevnet,
			address:         "addr-rollback-sol",
			preForkTxHash:   "sig-fork-old-sol",
			postForkTxHash:  "sig-fork-new-sol",
			preForkEventID:  "solana|devnet|sig-fork-old-sol|tx:outer:0:inner:-1|addr:addr-rollback-sol|asset:11111111111111111111111111111111|cat:TRANSFER",
			postForkEventID: "solana|devnet|sig-fork-new-sol|tx:outer:0:inner:-1|addr:addr-rollback-sol|asset:11111111111111111111111111111111|cat:TRANSFER",
			contractAddr:    "11111111111111111111111111111111",
			programID:       "11111111111111111111111111111111",
			counterparty:    "addr-counterparty-sol",
		},
		{
			name:            "base-sepolia",
			chain:           model.ChainBase,
			network:         model.NetworkSepolia,
			address:         "0x1111111111111111111111111111111111111111",
			preForkTxHash:   "0xbase_fork_old",
			postForkTxHash:  "0xbase_fork_new",
			preForkEventID:  "base|sepolia|0xbase_fork_old|tx:log:7|addr:0x1111111111111111111111111111111111111111|asset:ETH|cat:TRANSFER",
			postForkEventID: "base|sepolia|0xbase_fork_new|tx:log:7|addr:0x1111111111111111111111111111111111111111|asset:ETH|cat:TRANSFER",
			contractAddr:    "ETH",
			programID:       "0xbase-program",
			counterparty:    "0x2222222222222222222222222222222222222222",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
			_ = ctrl

			normalizedCh := make(chan event.NormalizedBatch, 1)
			ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

			for i := 0; i < 6; i++ {
				setupBeginTx(mockDB)
			}

			preForkTxID := uuid.New()
			postForkTxID := uuid.New()
			tokenID := uuid.New()
			preForkCursor := tc.preForkTxHash
			rewindCursor := "rewind-anchor"
			postForkCursor := tc.postForkTxHash

			buildBatch := func(prevCursor *string, prevSeq int64, txHash, eventID, finality, delta string, newCursor string, newSeq int64) event.NormalizedBatch {
				return event.NormalizedBatch{
					Chain:                  tc.chain,
					Network:                tc.network,
					Address:                tc.address,
					PreviousCursorValue:    prevCursor,
					PreviousCursorSequence: prevSeq,
					Transactions: []event.NormalizedTransaction{
						{
							TxHash:      txHash,
							BlockCursor: newSeq,
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
									ContractAddress:       tc.contractAddr,
									Address:               tc.address,
									CounterpartyAddress:   tc.counterparty,
									Delta:                 delta,
									EventID:               eventID,
									TokenType:             model.TokenTypeNative,
									FinalityState:         finality,
								},
							},
						},
					},
					NewCursorValue:    &newCursor,
					NewCursorSequence: newSeq,
				}
			}

			preForkConfirmed := buildBatch(nil, 0, tc.preForkTxHash, tc.preForkEventID, "confirmed", "-1", preForkCursor, 120)
			preForkFinalized := buildBatch(&preForkCursor, 120, tc.preForkTxHash, tc.preForkEventID, "finalized", "-1", preForkCursor, 120)
			rollback := event.NormalizedBatch{
				Chain:                  tc.chain,
				Network:                tc.network,
				Address:                tc.address,
				PreviousCursorValue:    &preForkCursor,
				PreviousCursorSequence: 120,
				NewCursorValue:         &rewindCursor,
				NewCursorSequence:      100,
			}
			postForkReplay := buildBatch(&rewindCursor, 100, tc.postForkTxHash, tc.postForkEventID, "finalized", "-2", postForkCursor, 110)
			rollbackRepeat := event.NormalizedBatch{
				Chain:                  tc.chain,
				Network:                tc.network,
				Address:                tc.address,
				PreviousCursorValue:    &postForkCursor,
				PreviousCursorSequence: 110,
				NewCursorValue:         &rewindCursor,
				NewCursorSequence:      100,
			}
			postForkReplayRepeat := buildBatch(&rewindCursor, 100, tc.postForkTxHash, tc.postForkEventID, "finalized", "-2", postForkCursor, 110)

			mockTxRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(preForkTxID, nil).
				Times(2)
			mockTxRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(postForkTxID, nil).
				Times(2)

			mockTokenRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(tokenID, nil).
				Times(4)

			upsertCalls := 0
			writtenEventIDs := make([]string, 0, 4)
			writtenFinality := make([]string, 0, 4)
			mockBERepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
					upsertCalls++
					writtenEventIDs = append(writtenEventIDs, be.EventID)
					writtenFinality = append(writtenFinality, be.FinalityState)
					switch upsertCalls {
					case 1:
						return true, nil
					case 2:
						return false, nil
					case 3:
						return true, nil
					case 4:
						return false, nil
					default:
						t.Fatalf("unexpected upsert call count: %d", upsertCalls)
						return false, nil
					}
				}).
				Times(4)

			appliedDeltas := make([]string, 0, 2)
			mockBalanceRepo.EXPECT().
				AdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					tokenID, nil, nil,
					gomock.Any(), gomock.Any(), gomock.Any(),
				).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, _ uuid.UUID, _ *string, _ *string, delta string, _ int64, _ string) error {
					appliedDeltas = append(appliedDeltas, delta)
					return nil
				}).
				Times(2)

			cursorWrites := make([]int64, 0, 4)
			mockCursorRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, tc.address, gomock.Any(), gomock.Any(), int64(1)).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, _ *string, cursorSeq int64, _ int64) error {
					cursorWrites = append(cursorWrites, cursorSeq)
					return nil
				}).
				Times(4)

			watermarkWrites := make([]int64, 0, 4)
			mockConfigRepo.EXPECT().
				UpdateWatermarkTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, ingestedSeq int64) error {
					watermarkWrites = append(watermarkWrites, ingestedSeq)
					return nil
				}).
				Times(4)

			rollbackForkSequences := make([]int64, 0, 2)
			ing.reorgHandler = func(_ context.Context, _ *sql.Tx, got event.NormalizedBatch) error {
				rollbackForkSequences = append(rollbackForkSequences, got.PreviousCursorSequence)
				return nil
			}

			require.NoError(t, ing.processBatch(context.Background(), preForkConfirmed))
			require.NoError(t, ing.processBatch(context.Background(), preForkFinalized))
			require.NoError(t, ing.processBatch(context.Background(), rollback))
			require.NoError(t, ing.processBatch(context.Background(), postForkReplay))
			require.NoError(t, ing.processBatch(context.Background(), rollbackRepeat))
			require.NoError(t, ing.processBatch(context.Background(), postForkReplayRepeat))

			assert.Equal(t, []int64{100, 100}, rollbackForkSequences)
			assert.Equal(t, []string{"-1", "-2"}, appliedDeltas)
			assert.Equal(t, []int64{120, 120, 110, 110}, cursorWrites)
			assert.Equal(t, []int64{120, 120, 110, 110}, watermarkWrites)
			assert.Equal(t, []string{tc.preForkEventID, tc.preForkEventID, tc.postForkEventID, tc.postForkEventID}, writtenEventIDs)
			assert.Equal(t, []string{"confirmed", "finalized", "finalized", "finalized"}, writtenFinality)
		})
	}
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

func TestProcessBatch_RestartFromBoundaryReplay_NoDoubleApplyAcrossMandatoryChains(t *testing.T) {
	testCases := []struct {
		name            string
		chain           model.Chain
		network         model.Network
		address         string
		txHash          string
		blockCursor     int64
		contractAddress string
		programID       string
		counterparty    string
		eventID         string
	}{
		{
			name:            "solana-devnet",
			chain:           model.ChainSolana,
			network:         model.NetworkDevnet,
			address:         "addr-restart-sol",
			txHash:          "sig-restart-sol-1",
			blockCursor:     333,
			contractAddress: "11111111111111111111111111111111",
			programID:       "11111111111111111111111111111111",
			counterparty:    "addr-restart-sol-counterparty",
			eventID:         "solana|devnet|sig-restart-sol-1|tx:outer:0:inner:-1|addr:addr-restart-sol|asset:11111111111111111111111111111111|cat:TRANSFER",
		},
		{
			name:            "base-sepolia",
			chain:           model.ChainBase,
			network:         model.NetworkSepolia,
			address:         "0x1111111111111111111111111111111111111111",
			txHash:          "0xrestart_base_1",
			blockCursor:     444,
			contractAddress: "ETH",
			programID:       "0xbase-program",
			counterparty:    "0x2222222222222222222222222222222222222222",
			eventID:         "base|sepolia|0xrestart_base_1|tx:log:7|addr:0x1111111111111111111111111111111111111111|asset:ETH|cat:TRANSFER",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
			_ = ctrl

			normalizedCh := make(chan event.NormalizedBatch, 1)
			ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

			txID := uuid.New()
			tokenID := uuid.New()
			cursorValue := tc.txHash

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
								Delta:                 "-1",
								EventID:               tc.eventID,
								TokenType:             model.TokenTypeNative,
							},
						},
					},
				},
				NewCursorValue:    &cursorValue,
				NewCursorSequence: tc.blockCursor,
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
				Times(2)

			insertedCount := 0
			mockBERepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
					assert.Equal(t, tc.chain, be.Chain)
					assert.Equal(t, tc.network, be.Network)
					assert.Equal(t, tc.txHash, be.TxHash)
					assert.Equal(t, tc.eventID, be.EventID)
					insertedCount++
					return insertedCount == 1, nil
				}).
				Times(2)

			mockBalanceRepo.EXPECT().
				AdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					tokenID, nil, nil,
					"-1", tc.blockCursor, tc.txHash,
				).
				Return(nil).
				Times(1)

			mockCursorRepo.EXPECT().
				UpsertTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					&cursorValue, tc.blockCursor, int64(1),
				).
				Return(nil).
				Times(2)

			mockConfigRepo.EXPECT().
				UpdateWatermarkTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, tc.blockCursor).
				Return(nil).
				Times(2)

			require.NoError(t, ing.processBatch(context.Background(), batch))
			require.NoError(t, ing.processBatch(context.Background(), batch))
			assert.Equal(t, 2, insertedCount, "boundary replay should upsert once then dedupe once")
		})
	}
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

func TestIngester_ProcessBatch_AmbiguousCommitAck_ReconcilesAcrossMandatoryChains(t *testing.T) {
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
		eventID         string
		commitErr       error
	}{
		{
			name:            "solana-devnet-ack-timeout",
			chain:           model.ChainSolana,
			network:         model.NetworkDevnet,
			address:         "addr-sol-commit-ambiguous",
			txHash:          "sig-sol-commit-ambiguous-1",
			blockCursor:     401,
			cursorValue:     "sig-sol-commit-ambiguous-1",
			contractAddress: "11111111111111111111111111111111",
			programID:       "11111111111111111111111111111111",
			counterparty:    "sol-counterparty",
			eventID:         "solana|devnet|sig-sol-commit-ambiguous-1|tx:outer:0:inner:-1|addr:addr-sol-commit-ambiguous|asset:11111111111111111111111111111111|cat:TRANSFER",
			commitErr:       errors.New("commit ack timeout"),
		},
		{
			name:            "solana-devnet-post-write-disconnect",
			chain:           model.ChainSolana,
			network:         model.NetworkDevnet,
			address:         "addr-sol-commit-disconnect",
			txHash:          "sig-sol-commit-disconnect-1",
			blockCursor:     402,
			cursorValue:     "sig-sol-commit-disconnect-1",
			contractAddress: "11111111111111111111111111111111",
			programID:       "11111111111111111111111111111111",
			counterparty:    "sol-counterparty",
			eventID:         "solana|devnet|sig-sol-commit-disconnect-1|tx:outer:0:inner:-1|addr:addr-sol-commit-disconnect|asset:11111111111111111111111111111111|cat:TRANSFER",
			commitErr:       errors.New("driver: bad connection"),
		},
		{
			name:            "base-sepolia-ack-timeout",
			chain:           model.ChainBase,
			network:         model.NetworkSepolia,
			address:         "0x1111111111111111111111111111111111111111",
			txHash:          "0xbase-commit-ambiguous-1",
			blockCursor:     501,
			cursorValue:     "0xbase-commit-ambiguous-1",
			contractAddress: "ETH",
			programID:       "0xbase-program",
			counterparty:    "0x2222222222222222222222222222222222222222",
			eventID:         "base|sepolia|0xbase-commit-ambiguous-1|tx:log:7|addr:0x1111111111111111111111111111111111111111|asset:ETH|cat:TRANSFER",
			commitErr:       errors.New("i/o timeout while waiting for commit ack"),
		},
		{
			name:            "base-sepolia-post-write-disconnect",
			chain:           model.ChainBase,
			network:         model.NetworkSepolia,
			address:         "0x3333333333333333333333333333333333333333",
			txHash:          "0xbase-commit-disconnect-1",
			blockCursor:     502,
			cursorValue:     "0xbase-commit-disconnect-1",
			contractAddress: "ETH",
			programID:       "0xbase-program",
			counterparty:    "0x4444444444444444444444444444444444444444",
			eventID:         "base|sepolia|0xbase-commit-disconnect-1|tx:log:8|addr:0x3333333333333333333333333333333333333333|asset:ETH|cat:TRANSFER",
			commitErr:       errors.New("connection reset by peer after commit"),
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clearFakeCommitErrors()
			t.Cleanup(clearFakeCommitErrors)
			setFakeCommitErrors(tc.commitErr)

			ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
			_ = ctrl

			ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, nil, slog.Default())

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
								Delta:                 "-1",
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

			mockTxRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(txID, nil).
				Times(1)

			mockTokenRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(tokenID, nil).
				Times(1)

			mockBERepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(true, nil).
				Times(1)

			mockBalanceRepo.EXPECT().
				AdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					tokenID, nil, nil,
					"-1", tc.blockCursor, tc.txHash,
				).
				Return(nil).
				Times(1)

			mockCursorRepo.EXPECT().
				UpsertTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					&tc.cursorValue, tc.blockCursor, int64(1),
				).
				Return(nil).
				Times(1)

			mockConfigRepo.EXPECT().
				UpdateWatermarkTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, tc.blockCursor).
				Return(nil).
				Times(1)

			mockCursorRepo.EXPECT().
				Get(gomock.Any(), tc.chain, tc.network, tc.address).
				Return(&model.AddressCursor{
					Chain:          tc.chain,
					Network:        tc.network,
					Address:        tc.address,
					CursorValue:    &tc.cursorValue,
					CursorSequence: tc.blockCursor,
				}, nil).
				Times(1)

			require.NoError(t, ing.processBatch(context.Background(), batch))
		})
	}
}

func TestIngester_ProcessBatch_AmbiguousCommitAck_PermutationsConvergeCanonicalTupleAcrossMandatoryChains(t *testing.T) {
	type canonicalTuple struct {
		EventID  string
		TxHash   string
		Address  string
		Category model.EventCategory
		Delta    string
	}
	type permutation struct {
		name      string
		commitErr error
	}

	testCases := []struct {
		name            string
		chain           model.Chain
		network         model.Network
		address         string
		txHashInput     string
		txHashExpected  string
		cursorInput     string
		cursorExpected  string
		blockCursor     int64
		contractAddress string
		programID       string
		counterparty    string
		eventID         string
	}{
		{
			name:            "solana-devnet",
			chain:           model.ChainSolana,
			network:         model.NetworkDevnet,
			address:         "addr-sol-permutation",
			txHashInput:     "sig-sol-permutation-1",
			txHashExpected:  "sig-sol-permutation-1",
			cursorInput:     "sig-sol-permutation-1",
			cursorExpected:  "sig-sol-permutation-1",
			blockCursor:     803,
			contractAddress: "11111111111111111111111111111111",
			programID:       "11111111111111111111111111111111",
			counterparty:    "sol-counterparty",
			eventID:         "solana|devnet|sig-sol-permutation-1|tx:outer:0:inner:-1|addr:addr-sol-permutation|asset:11111111111111111111111111111111|cat:TRANSFER",
		},
		{
			name:            "base-sepolia",
			chain:           model.ChainBase,
			network:         model.NetworkSepolia,
			address:         "0x7777777777777777777777777777777777777777",
			txHashInput:     "0xAbCdEf001122",
			txHashExpected:  "0xabcdef001122",
			cursorInput:     "0xAbCdEf001122",
			cursorExpected:  "0xabcdef001122",
			blockCursor:     903,
			contractAddress: "ETH",
			programID:       "0xbase-program",
			counterparty:    "0x8888888888888888888888888888888888888888",
			eventID:         "base|sepolia|0xabcdef001122|tx:log:11|addr:0x7777777777777777777777777777777777777777|asset:ETH|cat:TRANSFER",
		},
	}
	permutations := []permutation{
		{name: "ack-timeout", commitErr: errors.New("commit ack timeout")},
		{name: "post-write-disconnect", commitErr: errors.New("driver: bad connection")},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runPermutation := func(t *testing.T, p permutation) canonicalTuple {
				t.Helper()
				clearFakeCommitErrors()
				setFakeCommitErrors(p.commitErr)
				t.Cleanup(clearFakeCommitErrors)

				ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
				_ = ctrl

				ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, nil, slog.Default())

				txID := uuid.New()
				tokenID := uuid.New()
				batch := event.NormalizedBatch{
					Chain:   tc.chain,
					Network: tc.network,
					Address: tc.address,
					Transactions: []event.NormalizedTransaction{
						{
							TxHash:      tc.txHashInput,
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
									Delta:                 "-1",
									EventID:               tc.eventID,
									TokenType:             model.TokenTypeNative,
								},
							},
						},
					},
					NewCursorValue:    &tc.cursorInput,
					NewCursorSequence: tc.blockCursor,
				}

				setupBeginTx(mockDB)

				mockTxRepo.EXPECT().
					UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ *sql.Tx, tx *model.Transaction) (uuid.UUID, error) {
						assert.Equal(t, tc.txHashExpected, tx.TxHash)
						return txID, nil
					}).
					Times(1)

				mockTokenRepo.EXPECT().
					UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(tokenID, nil).
					Times(1)

				var tuple canonicalTuple
				mockBERepo.EXPECT().
					UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ *sql.Tx, be *model.BalanceEvent) (bool, error) {
						tuple = canonicalTuple{
							EventID:  be.EventID,
							TxHash:   be.TxHash,
							Address:  be.Address,
							Category: be.EventCategory,
							Delta:    be.Delta,
						}
						return true, nil
					}).
					Times(1)

				mockBalanceRepo.EXPECT().
					AdjustBalanceTx(
						gomock.Any(), gomock.Any(),
						tc.chain, tc.network, tc.address,
						tokenID, nil, nil,
						"-1", tc.blockCursor, tc.txHashExpected,
					).
					Return(nil).
					Times(1)

				mockCursorRepo.EXPECT().
					UpsertTx(
						gomock.Any(), gomock.Any(),
						tc.chain, tc.network, tc.address,
						&tc.cursorExpected, tc.blockCursor, int64(1),
					).
					Return(nil).
					Times(1)

				mockConfigRepo.EXPECT().
					UpdateWatermarkTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, tc.blockCursor).
					Return(nil).
					Times(1)

				mockCursorRepo.EXPECT().
					Get(gomock.Any(), tc.chain, tc.network, tc.address).
					Return(&model.AddressCursor{
						Chain:          tc.chain,
						Network:        tc.network,
						Address:        tc.address,
						CursorValue:    &tc.cursorExpected,
						CursorSequence: tc.blockCursor,
					}, nil).
					Times(1)

				require.NoError(t, ing.processBatch(context.Background(), batch))
				return tuple
			}

			timeoutTuple := runPermutation(t, permutations[0])
			disconnectTuple := runPermutation(t, permutations[1])
			assert.Equal(t, timeoutTuple, disconnectTuple, "ambiguous commit permutations must converge to an identical canonical tuple")
		})
	}
}

func TestIngester_ProcessBatch_AmbiguousCommitAck_CursorAheadFailsClosedAcrossMandatoryChains(t *testing.T) {
	testCases := []struct {
		name            string
		chain           model.Chain
		network         model.Network
		address         string
		txHash          string
		blockCursor     int64
		cursorValue     string
		observedCursor  string
		contractAddress string
		programID       string
		counterparty    string
		eventID         string
		commitErr       error
	}{
		{
			name:            "solana-devnet-cursor-ahead",
			chain:           model.ChainSolana,
			network:         model.NetworkDevnet,
			address:         "addr-sol-cursor-ahead",
			txHash:          "sig-sol-cursor-ahead-1",
			blockCursor:     1001,
			cursorValue:     "sig-sol-cursor-ahead-1",
			observedCursor:  "sig-sol-cursor-ahead-2",
			contractAddress: "11111111111111111111111111111111",
			programID:       "11111111111111111111111111111111",
			counterparty:    "sol-counterparty",
			eventID:         "solana|devnet|sig-sol-cursor-ahead-1|tx:outer:0:inner:-1|addr:addr-sol-cursor-ahead|asset:11111111111111111111111111111111|cat:TRANSFER",
			commitErr:       errors.New("commit ack timeout"),
		},
		{
			name:            "base-sepolia-cursor-ahead",
			chain:           model.ChainBase,
			network:         model.NetworkSepolia,
			address:         "0x9999999999999999999999999999999999999999",
			txHash:          "0xbasecursorahead01",
			blockCursor:     1101,
			cursorValue:     "0xbasecursorahead01",
			observedCursor:  "0xbasecursorahead02",
			contractAddress: "ETH",
			programID:       "0xbase-program",
			counterparty:    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			eventID:         "base|sepolia|0xbasecursorahead01|tx:log:12|addr:0x9999999999999999999999999999999999999999|asset:ETH|cat:TRANSFER",
			commitErr:       errors.New("connection reset by peer after commit"),
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clearFakeCommitErrors()
			t.Cleanup(clearFakeCommitErrors)
			setFakeCommitErrors(tc.commitErr)

			ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
			_ = ctrl

			ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, nil, slog.Default())

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
								Delta:                 "-1",
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
				Return(txID, nil).
				Times(2)

			mockTokenRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(tokenID, nil).
				Times(2)

			insertedCount := 0
			mockBERepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, *sql.Tx, *model.BalanceEvent) (bool, error) {
					if insertedCount == 0 {
						insertedCount++
						return true, nil
					}
					return false, nil
				}).
				Times(2)

			mockBalanceRepo.EXPECT().
				AdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					tokenID, nil, nil,
					"-1", tc.blockCursor, tc.txHash,
				).
				Return(nil).
				Times(1)

			cursorWrites := make([]int64, 0, 2)
			mockCursorRepo.EXPECT().
				UpsertTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					&tc.cursorValue, tc.blockCursor, int64(1),
				).
				DoAndReturn(func(context.Context, *sql.Tx, model.Chain, model.Network, string, *string, int64, int64) error {
					cursorWrites = append(cursorWrites, tc.blockCursor)
					return nil
				}).
				Times(2)

			mockConfigRepo.EXPECT().
				UpdateWatermarkTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, tc.blockCursor).
				Return(nil).
				Times(2)

			observedSequence := tc.blockCursor + 5
			mockCursorRepo.EXPECT().
				Get(gomock.Any(), tc.chain, tc.network, tc.address).
				Return(&model.AddressCursor{
					Chain:          tc.chain,
					Network:        tc.network,
					Address:        tc.address,
					CursorValue:    &tc.observedCursor,
					CursorSequence: observedSequence,
				}, nil).
				Times(1)

			err := ing.processBatch(context.Background(), batch)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "commit_ambiguity_unresolved")
			assert.Contains(t, err.Error(), "reason=cursor_ahead_ambiguous")

			// Replay after a fail-closed ambiguity decision must remain idempotent.
			require.NoError(t, ing.processBatch(context.Background(), batch))
			assert.Equal(t, 1, insertedCount, "replay should not duplicate canonical IDs when first commit outcome was ambiguous")
			require.Len(t, cursorWrites, 2)
			assert.GreaterOrEqual(t, cursorWrites[1], cursorWrites[0], "cursor progression must remain monotonic across ambiguous boundary replay")
		})
	}
}

func TestIngester_ProcessBatch_AmbiguousCommitRetryAfterUnknown_DeduplicatesAcrossMandatoryChains(t *testing.T) {
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
		eventID         string
		commitErr       error
	}{
		{
			name:            "solana-devnet-retry-after-unknown",
			chain:           model.ChainSolana,
			network:         model.NetworkDevnet,
			address:         "addr-sol-unknown-retry",
			txHash:          "sig-sol-unknown-retry-1",
			blockCursor:     601,
			cursorValue:     "sig-sol-unknown-retry-1",
			contractAddress: "11111111111111111111111111111111",
			programID:       "11111111111111111111111111111111",
			counterparty:    "sol-counterparty",
			eventID:         "solana|devnet|sig-sol-unknown-retry-1|tx:outer:0:inner:-1|addr:addr-sol-unknown-retry|asset:11111111111111111111111111111111|cat:TRANSFER",
			commitErr:       errors.New("commit ack timeout"),
		},
		{
			name:            "base-sepolia-retry-after-unknown",
			chain:           model.ChainBase,
			network:         model.NetworkSepolia,
			address:         "0x5555555555555555555555555555555555555555",
			txHash:          "0xbase-unknown-retry-1",
			blockCursor:     701,
			cursorValue:     "0xbase-unknown-retry-1",
			contractAddress: "ETH",
			programID:       "0xbase-program",
			counterparty:    "0x6666666666666666666666666666666666666666",
			eventID:         "base|sepolia|0xbase-unknown-retry-1|tx:log:9|addr:0x5555555555555555555555555555555555555555|asset:ETH|cat:TRANSFER",
			commitErr:       errors.New("driver: bad connection"),
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clearFakeCommitErrors()
			t.Cleanup(clearFakeCommitErrors)
			setFakeCommitErrors(tc.commitErr)

			ctrl, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)
			_ = ctrl

			ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, nil, slog.Default())
			ing.retryMaxAttempts = 3
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
								Delta:                 "-1",
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
				Return(txID, nil).
				Times(2)

			mockTokenRepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(tokenID, nil).
				Times(2)

			totalUpserts := 0
			insertedCount := 0
			mockBERepo.EXPECT().
				UpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, *sql.Tx, *model.BalanceEvent) (bool, error) {
					totalUpserts++
					if totalUpserts == 1 {
						insertedCount++
						return true, nil
					}
					return false, nil
				}).
				Times(2)

			mockBalanceRepo.EXPECT().
				AdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					tokenID, nil, nil,
					"-1", tc.blockCursor, tc.txHash,
				).
				Return(nil).
				Times(1)

			cursorWrites := make([]int64, 0, 2)
			mockCursorRepo.EXPECT().
				UpsertTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, tc.address,
					&tc.cursorValue, tc.blockCursor, int64(1),
				).
				DoAndReturn(func(context.Context, *sql.Tx, model.Chain, model.Network, string, *string, int64, int64) error {
					cursorWrites = append(cursorWrites, tc.blockCursor)
					return nil
				}).
				Times(2)

			mockConfigRepo.EXPECT().
				UpdateWatermarkTx(gomock.Any(), gomock.Any(), tc.chain, tc.network, tc.blockCursor).
				Return(nil).
				Times(2)

			mockCursorRepo.EXPECT().
				Get(gomock.Any(), tc.chain, tc.network, tc.address).
				Return(nil, errors.New("cursor probe unavailable")).
				Times(1)

			err := ing.processBatchWithRetry(context.Background(), batch)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "terminal_failure stage=ingester.process_batch")
			assert.Contains(t, err.Error(), "commit_ambiguity_unresolved")

			// Replay/resume after unknown outcome must remain idempotent.
			require.NoError(t, ing.processBatch(context.Background(), batch))

			assert.Equal(t, 2, totalUpserts)
			assert.Equal(t, 1, insertedCount, "retry-after-unknown replay must not duplicate canonical IDs")
			require.Len(t, cursorWrites, 2)
			assert.GreaterOrEqual(t, cursorWrites[1], cursorWrites[0], "cursor progression must stay monotonic on replay")
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
