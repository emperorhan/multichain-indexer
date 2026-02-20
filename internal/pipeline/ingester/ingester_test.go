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
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/retry"
	"github.com/emperorhan/multichain-indexer/internal/store"
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
func (tx *fakeTxImpl) Rollback() error { return nil }

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
		BulkGetAmountWithExistsTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
			result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
			for _, k := range keys {
				result[k] = store.BalanceInfo{Amount: "0", Exists: false}
			}
			return result, nil
		}).AnyTimes()
	mockTokenRepo := storemocks.NewMockTokenRepository(ctrl)
	mockTokenRepo.EXPECT().
		BulkIsDeniedTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, addrs []string) (map[string]bool, error) {
			result := make(map[string]bool, len(addrs))
			for _, addr := range addrs {
				result[addr] = false
			}
			return result, nil
		}).AnyTimes()
	return ctrl,
		storemocks.NewMockTxBeginner(ctrl),
		storemocks.NewMockTransactionRepository(ctrl),
		storemocks.NewMockBalanceEventRepository(ctrl),
		mockBalanceRepo,
		mockTokenRepo,
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

func (r *testIndexerConfigStateRepo) GetWatermark(_ context.Context, _ model.Chain, _ model.Network) (*model.PipelineWatermark, error) {
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
	assert.False(t, isCanonicalityDrift(event.NormalizedBatch{
		PreviousCursorValue:    &oldSig,
		NewCursorValue:         &newSig,
		PreviousCursorSequence: 10,
		NewCursorSequence:      10,
		Transactions: []event.NormalizedTransaction{
			{TxHash: "sig-overlap-1", BlockCursor: 10},
		},
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

func TestResolveRewindCursorBoundary(t *testing.T) {
	found := "found_rewind_cursor"
	committed := "committed_rewind_cursor"
	fallback := "fallback_rewind_cursor"

	t.Run("prefers_found_event_cursor", func(t *testing.T) {
		rewindValue, rewindSequence := resolveRewindCursorBoundary(
			model.ChainSolana,
			120,
			&found,
			120,
			&committed,
			90,
			&fallback,
			120,
		)
		require.NotNil(t, rewindValue)
		assert.Equal(t, found, *rewindValue)
		assert.Equal(t, int64(120), rewindSequence)
	})

	t.Run("uses_committed_cursor_if_falls_before_fork", func(t *testing.T) {
		rewindValue, rewindSequence := resolveRewindCursorBoundary(
			model.ChainSolana,
			120,
			nil,
			0,
			&committed,
			90,
			&fallback,
			120,
		)
		require.NotNil(t, rewindValue)
		assert.Equal(t, committed, *rewindValue)
		assert.Equal(t, int64(90), rewindSequence)
	})

	t.Run("falls_back_to_batch_boundary_when_no_commit_anchor", func(t *testing.T) {
		rewindValue, rewindSequence := resolveRewindCursorBoundary(
			model.ChainSolana,
			100,
			nil,
			0,
			&committed,
			200,
			&fallback,
			100,
		)
		require.NotNil(t, rewindValue)
		assert.Equal(t, fallback, *rewindValue)
		assert.Equal(t, int64(100), rewindSequence)
	})
}

func TestRollbackForkCursorSequence_BTCEarliestCompetingWindow(t *testing.T) {
	t.Run("non-btc-keeps-new-cursor-sequence", func(t *testing.T) {
		batch := event.NormalizedBatch{
			Chain:                  model.ChainSolana,
			PreviousCursorSequence: 102,
			NewCursorSequence:      101,
			Transactions: []event.NormalizedTransaction{
				{BlockCursor: 100},
				{BlockCursor: 101},
			},
		}
		assert.Equal(t, int64(101), rollbackForkCursorSequence(batch))
	})

	t.Run("btc-uses-earliest-replacement-sequence", func(t *testing.T) {
		batch := event.NormalizedBatch{
			Chain:                  model.ChainBTC,
			PreviousCursorSequence: 102,
			NewCursorSequence:      101,
			Transactions: []event.NormalizedTransaction{
				{BlockCursor: 100},
				{BlockCursor: 101},
			},
		}
		assert.Equal(t, int64(100), rollbackForkCursorSequence(batch))
	})

	t.Run("btc-falls-back-to-new-sequence-when-window-single-height", func(t *testing.T) {
		batch := event.NormalizedBatch{
			Chain:                  model.ChainBTC,
			PreviousCursorSequence: 102,
			NewCursorSequence:      101,
			Transactions: []event.NormalizedTransaction{
				{BlockCursor: 101},
			},
		}
		assert.Equal(t, int64(101), rollbackForkCursorSequence(batch))
	})
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
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, t := range txns {
				result[t.TxHash] = seedTxID
			}
			return result, nil
		})
	mockTokenRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(make(map[string]uuid.UUID), nil)

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
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, t := range txns {
				result[t.TxHash] = seedTxID
			}
			return result, nil
		})
	mockTokenRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(make(map[string]uuid.UUID), nil)

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
			canonicalFirst := identity.CanonicalSignatureIdentity(tc.chain, tc.firstSig)
			canonicalSecond := identity.CanonicalSignatureIdentity(tc.chain, tc.secondSig)
			canonicalThird := identity.CanonicalSignatureIdentity(tc.chain, tc.thirdSig)
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
					string(model.ActivityDeposit),
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

			previousCursor := identity.CanonicalSignatureIdentity(tc.chain, "cursor-prev")
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
								ActivityType:         model.ActivityDeposit,
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
								ActivityType:         model.ActivityDeposit,
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
								ActivityType:         model.ActivityDeposit,
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
								ActivityType:         model.ActivityDeposit,
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
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(txns))
					for _, t := range txns {
						result[t.TxHash] = txID
					}
					return result, nil
				}).
				Times(2)
			mockTokenRepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(tokens))
					for _, t := range tokens {
						result[t.ContractAddress] = tokenID
					}
					return result, nil
				}).
				Times(2)

			seenEventIDs := make(map[string]struct{})
			insertedEventIDs := make(map[string]struct{})
			mockBERepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
					inserted := 0
					for _, be := range events {
						if _, exists := seenEventIDs[be.EventID]; exists {
							continue
						}
						seenEventIDs[be.EventID] = struct{}{}
						insertedEventIDs[be.EventID] = struct{}{}
						inserted++
					}
					return store.BulkUpsertEventResult{InsertedCount: inserted}, nil
				}).
				Times(2)

			adjustments := 0
			mockBalanceRepo.EXPECT().
				BulkAdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, gomock.Any(),
				).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, items []store.BulkAdjustItem) error {
					adjustments += len(items)
					return nil
				}).
				Times(2)

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
			assert.Equal(t, 2, adjustments, "bulk adjust is called once per batch with aggregated deltas")
			require.Len(t, cursorWrites, 2)
			assert.GreaterOrEqual(t, cursorWrites[1], cursorWrites[0], "cursor progression must remain monotonic across deferred-recovery boundary")
		})
	}
}

func TestProcessBatch_LiveBackfillOverlapPermutationsConvergeAcrossMandatoryChains(t *testing.T) {
	type eventBlueprint struct {
		category model.ActivityType
		action   string
		delta    string
	}

	type testCase struct {
		name            string
		chain           model.Chain
		network         model.Network
		address         string
		contractAddress string
		programID       string
		counterparty    string
		tokenType       model.TokenType

		oldCursor string
		oldSeq    int64
		tx1Seq    int64
		tx2Seq    int64

		tx1Primary  string
		tx1Alias    string
		tx2APrimary string
		tx2AAlias   string
		tx2BPrimary string
		tx2BAlias   string

		events []eventBlueprint
	}

	type overlapResult struct {
		tupleKeys      map[string]struct{}
		eventIDs       map[string]struct{}
		categoryCounts map[model.ActivityType]int
		balanceByActor map[string]string

		cursorValue    string
		cursorSequence int64
		watermark      int64

		insertedCount  int
		upsertAttempts int

		cursorWrites    []int64
		watermarkWrites []int64
	}

	testCases := []testCase{
		{
			name:            "solana-devnet",
			chain:           model.ChainSolana,
			network:         model.NetworkDevnet,
			address:         "addr-live-backfill-sol",
			contractAddress: "11111111111111111111111111111111",
			programID:       "11111111111111111111111111111111",
			counterparty:    "addr-counterparty-sol",
			tokenType:       model.TokenTypeNative,
			oldCursor:       "sol-cursor-900",
			oldSeq:          900,
			tx1Seq:          901,
			tx2Seq:          902,
			tx1Primary:      "sol-lb-901",
			tx1Alias:        "sol-lb-901",
			tx2APrimary:     "sol-lb-902-a",
			tx2AAlias:       " sol-lb-902-a ",
			tx2BPrimary:     "sol-lb-902-b",
			tx2BAlias:       "sol-lb-902-b",
			events: []eventBlueprint{
				{category: model.ActivityDeposit, action: "transfer", delta: "-10"},
				{category: model.ActivityFee, action: "fee", delta: "-1"},
			},
		},
		{
			name:            "base-sepolia",
			chain:           model.ChainBase,
			network:         model.NetworkSepolia,
			address:         "0x1111111111111111111111111111111111111111",
			contractAddress: "ETH",
			programID:       "0xbase-program",
			counterparty:    "0x2222222222222222222222222222222222222222",
			tokenType:       model.TokenTypeNative,
			oldCursor:       "0xaaa1200",
			oldSeq:          1200,
			tx1Seq:          1201,
			tx2Seq:          1202,
			tx1Primary:      "0xAAA1201",
			tx1Alias:        "aaa1201",
			tx2APrimary:     "0xBBB1202",
			tx2AAlias:       "bbb1202",
			tx2BPrimary:     "0xCCC1202",
			tx2BAlias:       "ccc1202",
			events: []eventBlueprint{
				{category: model.ActivityDeposit, action: "native_transfer", delta: "-100"},
				{category: model.ActivityFeeExecutionL2, action: "fee_execution_l2", delta: "-3"},
				{category: model.ActivityFeeDataL1, action: "fee_data_l1", delta: "-1"},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			canonicalHash := func(hash string) string {
				canonical := identity.CanonicalSignatureIdentity(tc.chain, hash)
				if canonical != "" {
					return canonical
				}
				return strings.TrimSpace(hash)
			}

			buildTx := func(hash string, sequence int64) event.NormalizedTransaction {
				canonicalTx := canonicalHash(hash)
				events := make([]event.NormalizedBalanceEvent, 0, len(tc.events))
				for _, spec := range tc.events {
					eventID := strings.Join([]string{
						string(tc.chain),
						string(tc.network),
						canonicalTx,
						string(spec.category),
					}, "|")
					events = append(events, event.NormalizedBalanceEvent{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						ActivityType:         spec.category,
						EventAction:           spec.action,
						ProgramID:             tc.programID,
						ContractAddress:       tc.contractAddress,
						Address:               tc.address,
						CounterpartyAddress:   tc.counterparty,
						Delta:                 spec.delta,
						EventID:               eventID,
						TokenType:             tc.tokenType,
					})
				}

				return event.NormalizedTransaction{
					TxHash:        hash,
					BlockCursor:   sequence,
					FeeAmount:     "0",
					FeePayer:      tc.address,
					Status:        model.TxStatusSuccess,
					ChainData:     json.RawMessage("{}"),
					BalanceEvents: events,
				}
			}

			buildBatch := func(prevCursor string, prevSeq int64, newCursor string, newSeq int64, txs ...event.NormalizedTransaction) event.NormalizedBatch {
				prev := prevCursor
				next := newCursor
				return event.NormalizedBatch{
					Chain:                  tc.chain,
					Network:                tc.network,
					Address:                tc.address,
					PreviousCursorValue:    &prev,
					PreviousCursorSequence: prevSeq,
					NewCursorValue:         &next,
					NewCursorSequence:      newSeq,
					Transactions:           txs,
				}
			}

			runScenario := func(t *testing.T, batches []event.NormalizedBatch) overlapResult {
				t.Helper()

				state := newInterleaveState(nil)
				db := openFakeDB()
				t.Cleanup(func() {
					_ = db.Close()
				})

				ing := New(
					&interleaveTxBeginner{db: db},
					&interleaveTxRepo{state: state},
					&interleaveBalanceEventRepo{state: state},
					&interleaveBalanceRepo{state: state},
					&interleaveTokenRepo{state: state},
					&interleaveCursorRepo{state: state},
					&interleaveConfigRepo{state: state},
					nil,
					slog.Default(),
				)

				for _, batch := range batches {
					require.NoError(t, ing.processBatch(context.Background(), batch))
				}

				tuples := state.snapshotTuples()
				tupleKeys := make(map[string]struct{}, len(tuples))
				eventIDs := make(map[string]struct{}, len(tuples))
				categoryCounts := make(map[model.ActivityType]int)
				balanceByActor := make(map[string]string)
				for _, tuple := range tuples {
					tupleKeys[interleaveTupleKey(tuple)] = struct{}{}
					eventIDs[tuple.EventID] = struct{}{}
					categoryCounts[tuple.Category]++

					next, err := addDecimalStringsForTest(balanceByActor[tuple.Address], tuple.Delta)
					require.NoError(t, err)
					balanceByActor[tuple.Address] = next
				}

				chainKey := interleaveKey(tc.chain, tc.network)
				cursorKey := chainKey + "|" + tc.address

				cursors := state.snapshotCursors()
				watermarks := state.snapshotWatermarks()

				state.mu.Lock()
				insertedCount := len(state.insertedEvents)
				upsertAttempts := state.upsertAttempts
				cursorWrites := append([]int64(nil), state.cursorWrites[cursorKey]...)
				watermarkWrites := append([]int64(nil), state.watermarkWrites[chainKey]...)
				state.mu.Unlock()

				require.Len(t, cursors, 1)
				cursor, exists := cursors[cursorKey]
				require.True(t, exists)
				require.NotNil(t, cursor)
				require.NotNil(t, cursor.CursorValue)

				return overlapResult{
					tupleKeys:       tupleKeys,
					eventIDs:        eventIDs,
					categoryCounts:  categoryCounts,
					balanceByActor:  balanceByActor,
					cursorValue:     *cursor.CursorValue,
					cursorSequence:  cursor.CursorSequence,
					watermark:       watermarks[chainKey],
					insertedCount:   insertedCount,
					upsertAttempts:  upsertAttempts,
					cursorWrites:    cursorWrites,
					watermarkWrites: watermarkWrites,
				}
			}

			tx1Primary := buildTx(tc.tx1Primary, tc.tx1Seq)
			tx1Alias := buildTx(tc.tx1Alias, tc.tx1Seq)
			tx2APrimary := buildTx(tc.tx2APrimary, tc.tx2Seq)
			tx2AAlias := buildTx(tc.tx2AAlias, tc.tx2Seq)
			tx2BPrimary := buildTx(tc.tx2BPrimary, tc.tx2Seq)
			tx2BAlias := buildTx(tc.tx2BAlias, tc.tx2Seq)

			baseline := runScenario(t, []event.NormalizedBatch{
				buildBatch(tc.oldCursor, tc.oldSeq, tc.tx2BPrimary, tc.tx2Seq, tx1Primary, tx2APrimary, tx2BPrimary),
			})

			liveFirst := runScenario(t, []event.NormalizedBatch{
				buildBatch(tc.oldCursor, tc.oldSeq, tc.tx2APrimary, tc.tx2Seq, tx2APrimary),
				buildBatch(tc.tx2APrimary, tc.tx2Seq, tc.tx2BPrimary, tc.tx2Seq, tx1Alias, tx2AAlias, tx2BPrimary),
				buildBatch(tc.tx2BPrimary, tc.tx2Seq, tc.tx2BPrimary, tc.tx2Seq, tx2BAlias),
			})
			backfillFirst := runScenario(t, []event.NormalizedBatch{
				buildBatch(tc.oldCursor, tc.oldSeq, tc.tx2BPrimary, tc.tx2Seq, tx1Alias, tx2BPrimary),
				buildBatch(tc.tx2BPrimary, tc.tx2Seq, tc.tx2BPrimary, tc.tx2Seq, tx2AAlias, tx2BAlias),
				buildBatch(tc.tx2BPrimary, tc.tx2Seq, tc.tx2BPrimary, tc.tx2Seq, tx1Primary, tx2AAlias),
			})
			interleaved := runScenario(t, []event.NormalizedBatch{
				buildBatch(tc.oldCursor, tc.oldSeq, tc.tx2APrimary, tc.tx2Seq, tx2APrimary),
				buildBatch(tc.tx2APrimary, tc.tx2Seq, tc.tx2APrimary, tc.tx2Seq, tx1Alias),
				buildBatch(tc.tx2APrimary, tc.tx2Seq, tc.tx2BPrimary, tc.tx2Seq, tx2AAlias, tx2BPrimary),
				buildBatch(tc.tx2BPrimary, tc.tx2Seq, tc.tx2BPrimary, tc.tx2Seq, tx1Primary, tx2BAlias),
			})

			assert.Equal(t, baseline.tupleKeys, liveFirst.tupleKeys)
			assert.Equal(t, baseline.tupleKeys, backfillFirst.tupleKeys)
			assert.Equal(t, baseline.tupleKeys, interleaved.tupleKeys)

			assert.Equal(t, baseline.eventIDs, liveFirst.eventIDs)
			assert.Equal(t, baseline.eventIDs, backfillFirst.eventIDs)
			assert.Equal(t, baseline.eventIDs, interleaved.eventIDs)

			assert.Equal(t, baseline.categoryCounts, liveFirst.categoryCounts)
			assert.Equal(t, baseline.categoryCounts, backfillFirst.categoryCounts)
			assert.Equal(t, baseline.categoryCounts, interleaved.categoryCounts)

			assert.Equal(t, baseline.balanceByActor, liveFirst.balanceByActor)
			assert.Equal(t, baseline.balanceByActor, backfillFirst.balanceByActor)
			assert.Equal(t, baseline.balanceByActor, interleaved.balanceByActor)

			assert.Equal(t, baseline.cursorValue, liveFirst.cursorValue)
			assert.Equal(t, baseline.cursorValue, backfillFirst.cursorValue)
			assert.Equal(t, baseline.cursorValue, interleaved.cursorValue)
			assert.Equal(t, baseline.cursorSequence, liveFirst.cursorSequence)
			assert.Equal(t, baseline.cursorSequence, backfillFirst.cursorSequence)
			assert.Equal(t, baseline.cursorSequence, interleaved.cursorSequence)
			assert.Equal(t, baseline.watermark, liveFirst.watermark)
			assert.Equal(t, baseline.watermark, backfillFirst.watermark)
			assert.Equal(t, baseline.watermark, interleaved.watermark)

			assert.Equal(t, len(baseline.eventIDs), baseline.insertedCount)
			assert.Equal(t, len(baseline.eventIDs), liveFirst.insertedCount)
			assert.Equal(t, len(baseline.eventIDs), backfillFirst.insertedCount)
			assert.Equal(t, len(baseline.eventIDs), interleaved.insertedCount)

			assert.GreaterOrEqual(t, liveFirst.upsertAttempts, liveFirst.insertedCount)
			assert.GreaterOrEqual(t, backfillFirst.upsertAttempts, backfillFirst.insertedCount)
			assert.GreaterOrEqual(t, interleaved.upsertAttempts, interleaved.insertedCount)

			assertMonotonicWrites(t, map[string][]int64{"cursor": baseline.cursorWrites}, tc.name+" baseline cursor")
			assertMonotonicWrites(t, map[string][]int64{"cursor": liveFirst.cursorWrites}, tc.name+" live-first cursor")
			assertMonotonicWrites(t, map[string][]int64{"cursor": backfillFirst.cursorWrites}, tc.name+" backfill-first cursor")
			assertMonotonicWrites(t, map[string][]int64{"cursor": interleaved.cursorWrites}, tc.name+" interleaved cursor")
			assertMonotonicWrites(t, map[string][]int64{"watermark": baseline.watermarkWrites}, tc.name+" baseline watermark")
			assertMonotonicWrites(t, map[string][]int64{"watermark": liveFirst.watermarkWrites}, tc.name+" live-first watermark")
			assertMonotonicWrites(t, map[string][]int64{"watermark": backfillFirst.watermarkWrites}, tc.name+" backfill-first watermark")
			assertMonotonicWrites(t, map[string][]int64{"watermark": interleaved.watermarkWrites}, tc.name+" interleaved watermark")
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
						ActivityType:         model.ActivityDeposit,
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
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			require.Len(t, txns, 1)
			assert.Equal(t, canonicalTxHash, txns[0].TxHash)
			result := map[string]uuid.UUID{txns[0].TxHash: txID}
			return result, nil
		})

	mockTokenRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(tokens))
			for _, t := range tokens {
				result[t.ContractAddress] = tokenID
			}
			return result, nil
		})

	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			require.Len(t, events, 1)
			assert.Equal(t, canonicalTxHash, events[0].TxHash)
			return store.BulkUpsertEventResult{InsertedCount: 1}, nil
		})

	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(
			gomock.Any(), gomock.Any(),
			model.ChainBase, model.NetworkSepolia, gomock.Any(),
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
						ActivityType:         model.ActivityDeposit,
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

	mockTxRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, t := range txns {
				result[t.TxHash] = txID
			}
			return result, nil
		})

	mockTokenRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(tokens))
			for _, t := range tokens {
				result[t.ContractAddress] = tokenID
			}
			return result, nil
		})

	mockBERepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			require.Len(t, events, 1)
			be := events[0]
			assert.Equal(t, model.ChainSolana, be.Chain)
			assert.Equal(t, "addr1", be.Address)
			assert.Equal(t, "otherAddr", be.CounterpartyAddress)
			assert.Equal(t, "1000000", be.Delta)
			assert.Equal(t, model.ActivityDeposit, be.ActivityType)
			if assert.NotNil(t, be.BalanceBefore) {
				assert.Equal(t, "0", *be.BalanceBefore)
			}
			if assert.NotNil(t, be.BalanceAfter) {
				assert.Equal(t, "1000000", *be.BalanceAfter)
			}
			return store.BulkUpsertEventResult{InsertedCount: 1}, nil
		})

	// Positive delta for deposit
	mockBalanceRepo.EXPECT().BulkAdjustBalanceTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, gomock.Any(),
	).DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, items []store.BulkAdjustItem) error {
		require.Len(t, items, 1)
		assert.Equal(t, "addr1", items[0].Address)
		assert.Equal(t, "1000000", items[0].Delta)
		return nil
	})

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
						ActivityType:         model.ActivityDeposit,
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
						ActivityType:         model.ActivityFee,
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

	mockTxRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, t := range txns {
				result[t.TxHash] = txID
			}
			return result, nil
		})

	// Both events share the same ContractAddress, so bulk dedup produces ONE unique token
	mockTokenRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
			require.Len(t, tokens, 1)
			result := make(map[string]uuid.UUID, len(tokens))
			for _, tok := range tokens {
				result[tok.ContractAddress] = tokenID
			}
			return result, nil
		})

	// Two balance events in one bulk call
	mockBERepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			require.Len(t, events, 2)
			// First event: transfer
			assert.Equal(t, "-2000000", events[0].Delta)
			if assert.NotNil(t, events[0].BalanceBefore) {
				assert.Equal(t, "0", *events[0].BalanceBefore)
			}
			if assert.NotNil(t, events[0].BalanceAfter) {
				assert.Equal(t, "-2000000", *events[0].BalanceAfter)
			}
			// Second event: fee (in-memory balance tracks across events in same batch)
			assert.Equal(t, "-5000", events[1].Delta)
			assert.Equal(t, model.ActivityFee, events[1].ActivityType)
			if assert.NotNil(t, events[1].BalanceBefore) {
				assert.Equal(t, "-2000000", *events[1].BalanceBefore)
			}
			if assert.NotNil(t, events[1].BalanceAfter) {
				assert.Equal(t, "-2005000", *events[1].BalanceAfter)
			}
			return store.BulkUpsertEventResult{InsertedCount: 2}, nil
		})

	// Both events share the same (address, tokenID, balanceType) → aggregated into one adjustment
	mockBalanceRepo.EXPECT().BulkAdjustBalanceTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, gomock.Any(),
	).DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, items []store.BulkAdjustItem) error {
		require.Len(t, items, 1)
		assert.Equal(t, "addr1", items[0].Address)
		assert.Equal(t, "-2005000", items[0].Delta)
		return nil
	})

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

	mockTxRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, t := range txns {
				result[t.TxHash] = txID
			}
			return result, nil
		})

	// tokenRepo.BulkUpsertTx is still called with empty slice
	mockTokenRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(make(map[string]uuid.UUID), nil)

	// No balance event, token denied check, balance get, or balance adjust calls

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
						ActivityType:         model.ActivityDeposit,
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

	mockTxRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, t := range txns {
				result[t.TxHash] = txID
			}
			return result, nil
		})

	mockTokenRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(tokens))
			for _, t := range tokens {
				result[t.ContractAddress] = tokenID
			}
			return result, nil
		})

	mockBERepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(store.BulkUpsertEventResult{InsertedCount: 0}, nil)

	// Even for duplicate events, the bulk architecture always calls BulkAdjustBalanceTx
	// because Phase 3 aggregates deltas before Phase 4a checks for dedup
	mockBalanceRepo.EXPECT().BulkAdjustBalanceTx(
		gomock.Any(), gomock.Any(),
		model.ChainSolana, model.NetworkDevnet, gomock.Any(),
	).Return(nil)

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
									ActivityType:         model.ActivityDeposit,
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
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(txns))
					for _, t := range txns {
						result[t.TxHash] = txID
					}
					return result, nil
				}).
				Times(2)

			mockTokenRepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(tokens))
					for _, t := range tokens {
						result[t.ContractAddress] = tokenID
					}
					return result, nil
				}).
				Times(2)

			finalityWrites := make([]string, 0, 2)
			call := 0
			mockBERepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
					call++
					for _, be := range events {
						finalityWrites = append(finalityWrites, be.FinalityState)
					}
					if call == 1 {
						return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
					}
					return store.BulkUpsertEventResult{InsertedCount: 0}, nil
				}).
				Times(2)

			mockBalanceRepo.EXPECT().
				BulkAdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, gomock.Any(),
				).
				Return(nil).
				Times(2)

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
									ActivityType:         model.ActivityDeposit,
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

			txIDByHash := map[string]uuid.UUID{
				tc.preForkTxHash:  preForkTxID,
				tc.postForkTxHash: postForkTxID,
			}
			mockTxRepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(txns))
					for _, t := range txns {
						if id, ok := txIDByHash[t.TxHash]; ok {
							result[t.TxHash] = id
						} else {
							result[t.TxHash] = uuid.New()
						}
					}
					return result, nil
				}).
				Times(4)

			mockTokenRepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(tokens))
					for _, t := range tokens {
						result[t.ContractAddress] = tokenID
					}
					return result, nil
				}).
				Times(4)

			upsertCalls := 0
			writtenEventIDs := make([]string, 0, 4)
			writtenFinality := make([]string, 0, 4)
			mockBERepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
					upsertCalls++
					for _, be := range events {
						writtenEventIDs = append(writtenEventIDs, be.EventID)
						writtenFinality = append(writtenFinality, be.FinalityState)
					}
					switch upsertCalls {
					case 1:
						return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
					case 2:
						return store.BulkUpsertEventResult{InsertedCount: 0}, nil
					case 3:
						return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
					case 4:
						return store.BulkUpsertEventResult{InsertedCount: 0}, nil
					default:
						t.Fatalf("unexpected bulk upsert call count: %d", upsertCalls)
						return store.BulkUpsertEventResult{InsertedCount: 0}, nil
					}
				}).
				Times(4)

			appliedDeltas := make([]string, 0, 4)
			mockBalanceRepo.EXPECT().
				BulkAdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, gomock.Any(),
				).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, items []store.BulkAdjustItem) error {
					for _, item := range items {
						appliedDeltas = append(appliedDeltas, item.Delta)
					}
					return nil
				}).
				Times(4)

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
			assert.Equal(t, []string{"-1", "-1", "-2", "-2"}, appliedDeltas)
			assert.Equal(t, []int64{120, 120, 110, 110}, cursorWrites)
			assert.Equal(t, []int64{120, 120, 110, 110}, watermarkWrites)
			assert.Equal(t, []string{tc.preForkEventID, tc.preForkEventID, tc.postForkEventID, tc.postForkEventID}, writtenEventIDs)
			assert.Equal(t, []string{"confirmed", "finalized", "finalized", "finalized"}, writtenFinality)
		})
	}
}

func TestProcessBatch_BTCCompetingBranchReorgPermutationsConvergeDeterministically(t *testing.T) {
	type eventBlueprint struct {
		category model.ActivityType
		action   string
		path     string
		delta    string
	}

	type scenarioResult struct {
		tupleKeys             map[string]struct{}
		eventIDs              map[string]struct{}
		eventIDCounts         map[string]int
		totalDelta            string
		cursorValue           string
		cursorSequence        int64
		watermark             int64
		cursorWrites          []int64
		watermarkWrites       []int64
		rollbackForkSequences []int64
	}

	runID := "run-I-0730-M99-BTC-ROLLBACK-ANCHOR"
	recoveryPermutation := "restart_from_rollback_anchor"

	eventIDCountsUnique := func(eventIDCounts map[string]int) bool {
		for _, count := range eventIDCounts {
			if count != 1 {
				return false
			}
		}
		return true
	}

	eventSetEqual := func(a, b map[string]struct{}) bool {
		if len(a) != len(b) {
			return false
		}
		for key := range a {
			if _, exists := b[key]; !exists {
				return false
			}
		}
		return true
	}

	seqWritesMonotonic := func(values []int64) bool {
		for i := 1; i < len(values); i++ {
			if values[i] < values[i-1] {
				return false
			}
		}
		return true
	}

	eventIDFor := func(txHash, path string, category model.ActivityType) string {
		canonicalTx := identity.CanonicalSignatureIdentity(model.ChainBTC, txHash)
		return strings.Join([]string{
			string(model.ChainBTC),
			string(model.NetworkTestnet),
			canonicalTx,
			"path:" + path,
			"addr:btc-reorg-addr",
			"asset:BTC",
			"cat:" + string(category),
		}, "|")
	}

	buildTx := func(hash string, sequence int64, events []eventBlueprint) event.NormalizedTransaction {
		normalizedEvents := make([]event.NormalizedBalanceEvent, 0, len(events))
		for _, spec := range events {
			normalizedEvents = append(normalizedEvents, event.NormalizedBalanceEvent{
				OuterInstructionIndex: 0,
				InnerInstructionIndex: -1,
				ActivityType:         spec.category,
				EventAction:           spec.action,
				ProgramID:             "btc",
				ContractAddress:       "BTC",
				Address:               "btc-reorg-addr",
				CounterpartyAddress:   "btc-reorg-counterparty",
				Delta:                 spec.delta,
				EventID:               eventIDFor(hash, spec.path, spec.category),
				TokenType:             model.TokenTypeNative,
				EventPath:             spec.path,
				EventPathType:         "btc_utxo",
				AssetType:             "native",
				AssetID:               "BTC",
				FinalityState:         "confirmed",
				DecoderVersion:        "btc-decoder-v1",
				SchemaVersion:         "cex_v1",
			})
		}

		return event.NormalizedTransaction{
			TxHash:        hash,
			BlockCursor:   sequence,
			FeeAmount:     "0",
			FeePayer:      "btc-reorg-addr",
			Status:        model.TxStatusSuccess,
			ChainData:     json.RawMessage("{}"),
			BalanceEvents: normalizedEvents,
		}
	}

	buildBatch := func(prevCursor *string, prevSeq int64, nextCursor string, nextSeq int64, txs ...event.NormalizedTransaction) event.NormalizedBatch {
		next := nextCursor
		return event.NormalizedBatch{
			Chain:                  model.ChainBTC,
			Network:                model.NetworkTestnet,
			Address:                "btc-reorg-addr",
			PreviousCursorValue:    prevCursor,
			PreviousCursorSequence: prevSeq,
			NewCursorValue:         &next,
			NewCursorSequence:      nextSeq,
			Transactions:           txs,
		}
	}

	runScenario := func(t *testing.T, batches []event.NormalizedBatch) scenarioResult {
		t.Helper()

		state := newInterleaveState(nil)
		db := openFakeDB()
		t.Cleanup(func() { _ = db.Close() })

		cursorRepo := &interleaveCursorRepo{state: state}
		configRepo := &interleaveConfigRepo{state: state}

		ing := New(
			&interleaveTxBeginner{db: db},
			&interleaveTxRepo{state: state},
			&interleaveBalanceEventRepo{state: state},
			&interleaveBalanceRepo{state: state},
			&interleaveTokenRepo{state: state},
			cursorRepo,
			configRepo,
			nil,
			slog.Default(),
		)

		rollbackForkSequences := make([]int64, 0, 2)
		ing.reorgHandler = func(ctx context.Context, tx *sql.Tx, got event.NormalizedBatch) error {
			rollbackForkSequences = append(rollbackForkSequences, got.PreviousCursorSequence)
			rewindValue, rewindSequence, err := state.rollbackFromCursor(got.Chain, got.Network, got.Address, got.PreviousCursorSequence)
			if err != nil {
				return err
			}
			if err := cursorRepo.UpsertTx(ctx, tx, got.Chain, got.Network, got.Address, rewindValue, rewindSequence, 0); err != nil {
				return err
			}
			if err := configRepo.UpdateWatermarkTx(ctx, tx, got.Chain, got.Network, rewindSequence); err != nil {
				return err
			}
			return nil
		}

		for _, batch := range batches {
			require.NoError(t, ing.processBatch(context.Background(), batch))
		}

		tuples := state.snapshotTuples()
		tupleKeys := make(map[string]struct{}, len(tuples))
		eventIDs := make(map[string]struct{}, len(tuples))
		eventIDCounts := make(map[string]int, len(tuples))
		totalDelta := "0"
		for _, tuple := range tuples {
			tupleKeys[interleaveTupleKey(tuple)] = struct{}{}
			eventIDs[tuple.EventID] = struct{}{}
			eventIDCounts[tuple.EventID]++
			var err error
			totalDelta, err = addDecimalStringsForTest(totalDelta, tuple.Delta)
			require.NoError(t, err)
		}

		chainKey := interleaveKey(model.ChainBTC, model.NetworkTestnet)
		cursorKey := chainKey + "|btc-reorg-addr"
		cursors := state.snapshotCursors()
		watermarks := state.snapshotWatermarks()
		require.Contains(t, cursors, cursorKey)
		require.NotNil(t, cursors[cursorKey])
		require.NotNil(t, cursors[cursorKey].CursorValue)

		state.mu.Lock()
		cursorWrites := append([]int64(nil), state.cursorWrites[cursorKey]...)
		watermarkWrites := append([]int64(nil), state.watermarkWrites[chainKey]...)
		state.mu.Unlock()

		return scenarioResult{
			tupleKeys:             tupleKeys,
			eventIDs:              eventIDs,
			eventIDCounts:         eventIDCounts,
			totalDelta:            totalDelta,
			cursorValue:           *cursors[cursorKey].CursorValue,
			cursorSequence:        cursors[cursorKey].CursorSequence,
			watermark:             watermarks[chainKey],
			cursorWrites:          cursorWrites,
			watermarkWrites:       watermarkWrites,
			rollbackForkSequences: rollbackForkSequences,
		}
	}

	stable100 := buildTx("btc-stable-100", 100, []eventBlueprint{
		{category: model.ActivityDeposit, action: "vout_credit", path: "vout:0", delta: "10"},
		{category: model.ActivityFee, action: "miner_fee", path: "fee", delta: "-2"},
	})
	stable101 := buildTx("btc-stable-101", 101, []eventBlueprint{
		{category: model.ActivityDeposit, action: "vin_debit", path: "vin:0", delta: "-6"},
		{category: model.ActivityDeposit, action: "vout_credit", path: "vout:1", delta: "4"},
	})
	new102 := buildTx("btc-new-102", 102, []eventBlueprint{
		{category: model.ActivityDeposit, action: "vout_credit", path: "vout:0", delta: "3"},
		{category: model.ActivityFee, action: "miner_fee", path: "fee", delta: "-1"},
	})
	old100 := buildTx("btc-old-100", 100, []eventBlueprint{
		{category: model.ActivityDeposit, action: "vout_credit", path: "vout:0", delta: "7"},
		{category: model.ActivityFee, action: "miner_fee", path: "fee", delta: "-1"},
	})
	old101 := buildTx("btc-old-101", 101, []eventBlueprint{
		{category: model.ActivityDeposit, action: "vin_debit", path: "vin:0", delta: "-4"},
		{category: model.ActivityDeposit, action: "vout_credit", path: "vout:1", delta: "1"},
	})
	old102 := buildTx("btc-old-102", 102, []eventBlueprint{
		{category: model.ActivityDeposit, action: "vout_credit", path: "vout:0", delta: "9"},
		{category: model.ActivityFee, action: "miner_fee", path: "fee", delta: "-4"},
	})

	cursor99 := "btc-cursor-99"
	stable101Cursor := "btc-stable-101"
	old102Cursor := "btc-old-102"
	new102Cursor := "btc-new-102"
	restartAnchorCursor := "btc-restart-anchor-102"

	baseline := runScenario(t, []event.NormalizedBatch{
		buildBatch(&cursor99, 99, stable101Cursor, 101, stable100, stable101),
		buildBatch(&stable101Cursor, 101, new102Cursor, 102, new102),
	})

	oneBlockReorg := runScenario(t, []event.NormalizedBatch{
		buildBatch(&cursor99, 99, old102Cursor, 102, stable100, stable101, old102),
		buildBatch(&old102Cursor, 102, stable101Cursor, 101, stable101),
		buildBatch(&stable101Cursor, 101, new102Cursor, 102, new102),
	})

	multiBlockReorg := runScenario(t, []event.NormalizedBatch{
		buildBatch(&cursor99, 99, old102Cursor, 102, old100, old101, old102),
		buildBatch(&old102Cursor, 102, stable101Cursor, 101, stable100, stable101),
		buildBatch(&stable101Cursor, 101, new102Cursor, 102, new102),
	})
	restartFromRollbackAnchor := runScenario(t, []event.NormalizedBatch{
		buildBatch(&cursor99, 99, new102Cursor, 102, stable100, stable101),
		buildBatch(&new102Cursor, 102, new102Cursor, 102, new102),
		buildBatch(&new102Cursor, 102, restartAnchorCursor, 102),
		buildBatch(&restartAnchorCursor, 102, new102Cursor, 102, new102),
	})

	assertAllEventIDsUnique := func(t *testing.T, scenario string, counts map[string]int) {
		t.Helper()
		for eventID, count := range counts {
			assert.Equalf(t, 1, count, "event_id %s repeated in %s", eventID, scenario)
		}
	}

	assertAllEventIDsUnique(t, "baseline", baseline.eventIDCounts)
	assertAllEventIDsUnique(t, "one-block reorg", oneBlockReorg.eventIDCounts)
	assertAllEventIDsUnique(t, "multi-block reorg", multiBlockReorg.eventIDCounts)
	assertAllEventIDsUnique(t, "restart-anchor", restartFromRollbackAnchor.eventIDCounts)

	assert.Equal(t, baseline.tupleKeys, oneBlockReorg.tupleKeys)
	assert.Equal(t, baseline.tupleKeys, multiBlockReorg.tupleKeys)
	assert.Equal(t, baseline.tupleKeys, restartFromRollbackAnchor.tupleKeys)
	assert.Equal(t, baseline.eventIDs, oneBlockReorg.eventIDs)
	assert.Equal(t, baseline.eventIDs, multiBlockReorg.eventIDs)
	assert.Equal(t, baseline.eventIDs, restartFromRollbackAnchor.eventIDs)
	assert.Equal(t, "8", baseline.totalDelta)
	assert.Equal(t, baseline.totalDelta, oneBlockReorg.totalDelta)
	assert.Equal(t, baseline.totalDelta, multiBlockReorg.totalDelta)
	assert.Equal(t, baseline.totalDelta, restartFromRollbackAnchor.totalDelta)

	// Orphaned branch events must not survive rollback.
	assert.NotContains(t, oneBlockReorg.eventIDs, eventIDFor("btc-old-102", "vout:0", model.ActivityDeposit))
	assert.NotContains(t, oneBlockReorg.eventIDs, eventIDFor("btc-old-102", "fee", model.ActivityFee))
	assert.NotContains(t, multiBlockReorg.eventIDs, eventIDFor("btc-old-100", "vout:0", model.ActivityDeposit))
	assert.NotContains(t, multiBlockReorg.eventIDs, eventIDFor("btc-old-101", "vin:0", model.ActivityDeposit))
	assert.NotContains(t, multiBlockReorg.eventIDs, eventIDFor("btc-old-102", "vout:0", model.ActivityDeposit))
	assert.NotContains(t, restartFromRollbackAnchor.eventIDs, eventIDFor("btc-old-100", "vout:0", model.ActivityDeposit))
	assert.NotContains(t, restartFromRollbackAnchor.eventIDs, eventIDFor("btc-old-101", "vin:0", model.ActivityDeposit))
	assert.NotContains(t, restartFromRollbackAnchor.eventIDs, eventIDFor("btc-old-102", "vout:0", model.ActivityDeposit))
	assert.NotContains(t, restartFromRollbackAnchor.eventIDs, eventIDFor("btc-old-102", "fee", model.ActivityFee))

	// Replacement-branch emissions are one-time even across rollback + replay.
	assert.Equal(t, 1, oneBlockReorg.eventIDCounts[eventIDFor("btc-stable-101", "vin:0", model.ActivityDeposit)])
	assert.Equal(t, 1, oneBlockReorg.eventIDCounts[eventIDFor("btc-new-102", "vout:0", model.ActivityDeposit)])
	assert.Equal(t, 1, multiBlockReorg.eventIDCounts[eventIDFor("btc-stable-100", "vout:0", model.ActivityDeposit)])
	assert.Equal(t, 1, multiBlockReorg.eventIDCounts[eventIDFor("btc-new-102", "vout:0", model.ActivityDeposit)])
	assert.Equal(t, 1, restartFromRollbackAnchor.eventIDCounts[eventIDFor("btc-stable-100", "vout:0", model.ActivityDeposit)])
	assert.Equal(t, 1, restartFromRollbackAnchor.eventIDCounts[eventIDFor("btc-stable-101", "vin:0", model.ActivityDeposit)])
	assert.Equal(t, 1, restartFromRollbackAnchor.eventIDCounts[eventIDFor("btc-new-102", "vout:0", model.ActivityDeposit)])
	assert.Equal(t, 1, restartFromRollbackAnchor.eventIDCounts[eventIDFor("btc-stable-100", "fee", model.ActivityFee)])
	assert.Equal(t, 1, restartFromRollbackAnchor.eventIDCounts[eventIDFor("btc-new-102", "fee", model.ActivityFee)])

	canonicalEventIDUniqueOK := eventIDCountsUnique(baseline.eventIDCounts) &&
		eventIDCountsUnique(oneBlockReorg.eventIDCounts) &&
		eventIDCountsUnique(multiBlockReorg.eventIDCounts) &&
		eventIDCountsUnique(restartFromRollbackAnchor.eventIDCounts)
	replayIdempotentOK := eventSetEqual(baseline.eventIDs, oneBlockReorg.eventIDs) &&
		eventSetEqual(baseline.eventIDs, multiBlockReorg.eventIDs) &&
		eventSetEqual(baseline.eventIDs, restartFromRollbackAnchor.eventIDs)
	cursorMonotonicOK := seqWritesMonotonic(restartFromRollbackAnchor.cursorWrites) &&
		seqWritesMonotonic(restartFromRollbackAnchor.watermarkWrites)
	signedDeltaConservationOK := baseline.totalDelta == oneBlockReorg.totalDelta &&
		baseline.totalDelta == multiBlockReorg.totalDelta &&
		baseline.totalDelta == restartFromRollbackAnchor.totalDelta
	reorgRecoveryDeterministicOK := eventSetEqual(baseline.tupleKeys, oneBlockReorg.tupleKeys) &&
		eventSetEqual(baseline.tupleKeys, multiBlockReorg.tupleKeys) &&
		eventSetEqual(baseline.tupleKeys, restartFromRollbackAnchor.tupleKeys)
	chainAdapterRuntimeWiredOK := restartFromRollbackAnchor.cursorSequence == 102 &&
		restartFromRollbackAnchor.cursorValue == identity.CanonicalSignatureIdentity(model.ChainBTC, "btc-new-102") &&
		restartFromRollbackAnchor.watermark == 102

	assert.True(t, canonicalEventIDUniqueOK, "%s | %s canonical_event_id_unique_ok", runID, recoveryPermutation)
	assert.True(t, replayIdempotentOK, "%s | %s replay_idempotent_ok", runID, recoveryPermutation)
	assert.True(t, cursorMonotonicOK, "%s | %s cursor_monotonic_ok", runID, recoveryPermutation)
	assert.True(t, signedDeltaConservationOK, "%s | %s signed_delta_conservation_ok", runID, recoveryPermutation)
	assert.True(t, reorgRecoveryDeterministicOK, "%s | %s reorg_recovery_deterministic_ok", runID, recoveryPermutation)
	assert.True(t, chainAdapterRuntimeWiredOK, "%s | %s chain_adapter_runtime_wired_ok", runID, recoveryPermutation)

	expectedCursor := identity.CanonicalSignatureIdentity(model.ChainBTC, "btc-new-102")
	assert.Equal(t, expectedCursor, baseline.cursorValue)
	assert.Equal(t, expectedCursor, oneBlockReorg.cursorValue)
	assert.Equal(t, expectedCursor, multiBlockReorg.cursorValue)
	assert.Equal(t, expectedCursor, restartFromRollbackAnchor.cursorValue)
	assert.Equal(t, int64(102), baseline.cursorSequence)
	assert.Equal(t, int64(102), oneBlockReorg.cursorSequence)
	assert.Equal(t, int64(102), multiBlockReorg.cursorSequence)
	assert.Equal(t, int64(102), restartFromRollbackAnchor.cursorSequence)
	assert.Equal(t, int64(102), baseline.watermark)
	assert.Equal(t, int64(102), oneBlockReorg.watermark)
	assert.Equal(t, int64(102), multiBlockReorg.watermark)
	assert.Equal(t, int64(102), restartFromRollbackAnchor.watermark)

	assert.Equal(t, []int64{101, 102}, baseline.cursorWrites)
	assert.Equal(t, []int64{102, 100, 101, 102}, oneBlockReorg.cursorWrites)
	assert.Equal(t, []int64{102, 0, 101, 102}, multiBlockReorg.cursorWrites)
	assert.Equal(t, []int64{102, 102, 102, 102}, restartFromRollbackAnchor.cursorWrites)
	assert.Equal(t, []int64{101, 102}, baseline.watermarkWrites)
	assert.Equal(t, []int64{102, 102, 102, 102}, oneBlockReorg.watermarkWrites)
	assert.Equal(t, []int64{102, 102, 102, 102}, multiBlockReorg.watermarkWrites)
	assert.Equal(t, []int64{102, 102, 102, 102}, restartFromRollbackAnchor.watermarkWrites)
	assertMonotonicWrites(t, map[string][]int64{"one-block": oneBlockReorg.watermarkWrites}, "btc one-block watermark")
	assertMonotonicWrites(t, map[string][]int64{"multi-block": multiBlockReorg.watermarkWrites}, "btc multi-block watermark")
	assertMonotonicWrites(t, map[string][]int64{"rollback-anchor": restartFromRollbackAnchor.watermarkWrites}, "btc rollback-anchor watermark")
	assertMonotonicWrites(t, map[string][]int64{"restart-anchor cursor": restartFromRollbackAnchor.cursorWrites}, "btc rollback-anchor cursor")

	assert.Equal(t, []int64{101}, oneBlockReorg.rollbackForkSequences)
	assert.Equal(t, []int64{100}, multiBlockReorg.rollbackForkSequences)
	assert.Empty(t, restartFromRollbackAnchor.rollbackForkSequences)
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
						ActivityType:         model.ActivityDeposit,
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
						ActivityType:         model.ActivityFeeExecutionL2,
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
						ActivityType:         model.ActivityFeeDataL1,
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
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, t := range txns {
				result[t.TxHash] = txID
			}
			return result, nil
		}).
		Times(2)

	mockTokenRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(tokens))
			for _, t := range tokens {
				result[t.ContractAddress] = tokenID
			}
			return result, nil
		}).
		Times(2)

	upsertEventCalls := 0
	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
			upsertEventCalls++
			for _, be := range events {
				assert.Equal(t, model.ChainBase, be.Chain)
				assert.Equal(t, model.NetworkSepolia, be.Network)
				assert.Equal(t, "0xbase_tx_1", be.TxHash)
			}
			if upsertEventCalls == 1 {
				return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
			}
			return store.BulkUpsertEventResult{InsertedCount: 0}, nil
		}).
		Times(2)

	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(
			gomock.Any(),
			gomock.Any(),
			model.ChainBase,
			model.NetworkSepolia,
			gomock.Any(),
		).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, items []store.BulkAdjustItem) error {
			for _, item := range items {
				assert.True(t, strings.HasPrefix(item.Delta, "-"))
			}
			return nil
		}).
		Times(2)

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
								ActivityType:         model.ActivityDeposit,
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
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(txns))
					for _, t := range txns {
						result[t.TxHash] = txID
					}
					return result, nil
				}).
				Times(2)

			mockTokenRepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(tokens))
					for _, t := range tokens {
						result[t.ContractAddress] = tokenID
					}
					return result, nil
				}).
				Times(2)

			bulkUpsertCalls := 0
			mockBERepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
					bulkUpsertCalls++
					for _, be := range events {
						assert.Equal(t, tc.chain, be.Chain)
						assert.Equal(t, tc.network, be.Network)
						assert.Equal(t, tc.txHash, be.TxHash)
						assert.Equal(t, tc.eventID, be.EventID)
					}
					if bulkUpsertCalls == 1 {
						return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
					}
					return store.BulkUpsertEventResult{InsertedCount: 0}, nil
				}).
				Times(2)

			mockBalanceRepo.EXPECT().
				BulkAdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, gomock.Any(),
				).
				Return(nil).
				Times(2)

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
			assert.Equal(t, 2, bulkUpsertCalls, "boundary replay should bulk upsert once per batch")
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
		Category model.ActivityType
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
								ActivityType:         model.ActivityDeposit,
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
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
					for _, tx := range txns {
						assert.Equal(t, tc.chain, tx.Chain)
						assert.Equal(t, tc.network, tx.Network)
						assert.Equal(t, tc.txHash, tx.TxHash)
					}
					result := make(map[string]uuid.UUID, len(txns))
					for _, tx := range txns {
						result[tx.TxHash] = txID
					}
					return result, nil
				}).
				Times(2)

			mockTokenRepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
					for _, token := range tokens {
						assert.Equal(t, tc.chain, token.Chain)
						assert.Equal(t, tc.network, token.Network)
						assert.Equal(t, tc.contractAddress, token.ContractAddress)
					}
					result := make(map[string]uuid.UUID, len(tokens))
					for _, token := range tokens {
						result[token.ContractAddress] = tokenID
					}
					return result, nil
				}).
				Times(2)

			tuples := make([]canonicalTuple, 0, 2)
			mockBERepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
					for _, be := range events {
						tuples = append(tuples, canonicalTuple{
							EventID:  be.EventID,
							TxHash:   be.TxHash,
							Address:  be.Address,
							Category: be.ActivityType,
							Delta:    be.Delta,
						})
					}
					return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
				}).
				Times(2)

			mockBalanceRepo.EXPECT().
				BulkAdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, gomock.Any(),
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
								ActivityType:         model.ActivityDeposit,
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
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(txns))
					for _, t := range txns {
						result[t.TxHash] = txID
					}
					return result, nil
				}).
				Times(1)

			mockTokenRepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(tokens))
					for _, t := range tokens {
						result[t.ContractAddress] = tokenID
					}
					return result, nil
				}).
				Times(1)

			mockBERepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(store.BulkUpsertEventResult{InsertedCount: 1}, nil).
				Times(1)

			mockBalanceRepo.EXPECT().
				BulkAdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, gomock.Any(),
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
		Category model.ActivityType
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
									ActivityType:         model.ActivityDeposit,
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
					BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
						require.Len(t, txns, 1)
						assert.Equal(t, tc.txHashExpected, txns[0].TxHash)
						return map[string]uuid.UUID{txns[0].TxHash: txID}, nil
					}).
					Times(1)

				mockTokenRepo.EXPECT().
					BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
						result := make(map[string]uuid.UUID, len(tokens))
						for _, t := range tokens {
							result[t.ContractAddress] = tokenID
						}
						return result, nil
					}).
					Times(1)

				var tuple canonicalTuple
				mockBERepo.EXPECT().
					BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
						require.Len(t, events, 1)
						be := events[0]
						tuple = canonicalTuple{
							EventID:  be.EventID,
							TxHash:   be.TxHash,
							Address:  be.Address,
							Category: be.ActivityType,
							Delta:    be.Delta,
						}
						return store.BulkUpsertEventResult{InsertedCount: 1}, nil
					}).
					Times(1)

				mockBalanceRepo.EXPECT().
					BulkAdjustBalanceTx(
						gomock.Any(), gomock.Any(),
						tc.chain, tc.network, gomock.Any(),
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
								ActivityType:         model.ActivityDeposit,
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
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(txns))
					for _, t := range txns {
						result[t.TxHash] = txID
					}
					return result, nil
				}).
				Times(2)

			mockTokenRepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(tokens))
					for _, t := range tokens {
						result[t.ContractAddress] = tokenID
					}
					return result, nil
				}).
				Times(2)

			insertedCount := 0
			mockBERepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
					if insertedCount == 0 {
						insertedCount++
						return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
					}
					return store.BulkUpsertEventResult{InsertedCount: 0}, nil
				}).
				Times(2)

			mockBalanceRepo.EXPECT().
				BulkAdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, gomock.Any(),
				).
				Return(nil).
				Times(2)

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
								ActivityType:         model.ActivityDeposit,
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
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(txns))
					for _, t := range txns {
						result[t.TxHash] = txID
					}
					return result, nil
				}).
				Times(2)

			mockTokenRepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
					result := make(map[string]uuid.UUID, len(tokens))
					for _, t := range tokens {
						result[t.ContractAddress] = tokenID
					}
					return result, nil
				}).
				Times(2)

			totalUpserts := 0
			insertedCount := 0
			mockBERepo.EXPECT().
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
					totalUpserts++
					if totalUpserts == 1 {
						insertedCount += len(events)
						return store.BulkUpsertEventResult{InsertedCount: len(events)}, nil
					}
					return store.BulkUpsertEventResult{InsertedCount: 0}, nil
				}).
				Times(2)

			mockBalanceRepo.EXPECT().
				BulkAdjustBalanceTx(
					gomock.Any(), gomock.Any(),
					tc.chain, tc.network, gomock.Any(),
				).
				Return(nil).
				Times(2)

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
		{
			name:      "btc-testnet",
			chain:     model.ChainBTC,
			network:   model.NetworkTestnet,
			address:   "tb1-ingester-terminal",
			txHash:    "0xBTC-terminal-1",
			txCursor:  303,
			txStatus:  model.TxStatusSuccess,
			feePayer:  "tb1-ingester-terminal",
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
				BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(nil, retry.Terminal(errors.New("constraint violation"))).
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

	mockTxRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, t := range txns {
				result[t.TxHash] = txID
			}
			return result, nil
		})

	mockTokenRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(make(map[string]uuid.UUID), nil)

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

	mockTxRepo.EXPECT().BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("constraint violation"))

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bulk upsert transactions")
}

func TestProcessBatch_FailFastDoesNotAdvanceCursorOrWatermarkOnBalanceTransitionError(t *testing.T) {
	_, mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo := newIngesterMocks(t)

	normalizedCh := make(chan event.NormalizedBatch, 1)
	ing := New(mockDB, mockTxRepo, mockBERepo, mockBalanceRepo, mockTokenRepo, mockCursorRepo, mockConfigRepo, normalizedCh, slog.Default())

	txID := uuid.New()
	tokenID := uuid.New()
	cursorVal := "sig1"

	batch := event.NormalizedBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: "addr1",
		Transactions: []event.NormalizedTransaction{
			{
				TxHash:      "sig1",
				BlockCursor: 100,
				FeeAmount:   "0",
				FeePayer:    "other",
				Status:      model.TxStatusSuccess,
				ChainData:   json.RawMessage("{}"),
				BalanceEvents: []event.NormalizedBalanceEvent{
					{
						OuterInstructionIndex: 0,
						InnerInstructionIndex: -1,
						ActivityType:         model.ActivityDeposit,
						EventAction:           "transfer",
						ProgramID:             "11111111111111111111111111111111",
						ContractAddress:       "11111111111111111111111111111111",
						Address:               "addr1",
						CounterpartyAddress:   "counterparty",
						Delta:                 "-1",
						TokenType:             model.TokenTypeNative,
					},
				},
			},
		},
		NewCursorValue:    &cursorVal,
		NewCursorSequence: 100,
	}

	setupBeginTx(mockDB)

	mockTxRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(txns))
			for _, t := range txns {
				result[t.TxHash] = txID
			}
			return result, nil
		}).
		Times(1)

	mockTokenRepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
			result := make(map[string]uuid.UUID, len(tokens))
			for _, t := range tokens {
				result[t.ContractAddress] = tokenID
			}
			return result, nil
		}).
		Times(1)

	mockBERepo.EXPECT().
		BulkUpsertTx(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(store.BulkUpsertEventResult{InsertedCount: 1}, nil).
		Times(1)

	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(
			gomock.Any(), gomock.Any(),
			model.ChainSolana, model.NetworkDevnet, gomock.Any(),
		).
		Return(errors.New("insufficient balance for adjustment")).
		Times(1)

	mockCursorRepo.EXPECT().
		UpsertTx(
			gomock.Any(), gomock.Any(),
			gomock.Any(), gomock.Any(), gomock.Any(),
			gomock.Any(), gomock.Any(), gomock.Any(),
		).
		Times(0)

	mockConfigRepo.EXPECT().
		UpdateWatermarkTx(
			gomock.Any(), gomock.Any(),
			gomock.Any(), gomock.Any(), gomock.Any(),
		).
		Times(0)

	err := ing.processBatch(context.Background(), batch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bulk adjust balances")
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

func TestIngester_Run_ReturnsErrorOnProcessBatchFailure(t *testing.T) {
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

	err := ing.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ingester process batch failed")
	assert.Contains(t, err.Error(), "db down")
}

func TestCanonicalSignatureIdentity_BTC(t *testing.T) {
	assert.Equal(t, "abcdef0011", identity.CanonicalSignatureIdentity(model.ChainBTC, "ABCDEF0011"))
	assert.Equal(t, "abcdef0011", identity.CanonicalSignatureIdentity(model.ChainBTC, "0xABCDEF0011"))
}
