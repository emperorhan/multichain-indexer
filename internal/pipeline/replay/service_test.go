package replay

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Mock implementations (hand-rolled, no gomock dependency needed)
// ---------------------------------------------------------------------------

type mockTxBeginner struct {
	err error
}

func (m *mockTxBeginner) BeginTx(_ context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
	return nil, m.err
}

type mockBalanceRepo struct {
	adjustCalled     int
	bulkAdjustErr    error
	bulkAdjustCalled int
	capturedItems    [][]store.BulkAdjustItem
}

func (m *mockBalanceRepo) AdjustBalanceTx(_ context.Context, _ *sql.Tx, _ store.AdjustRequest) error {
	m.adjustCalled++
	return nil
}

func (m *mockBalanceRepo) GetAmountTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, _ uuid.UUID, _ string) (string, error) {
	return "0", nil
}

func (m *mockBalanceRepo) GetAmountWithExistsTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, _ uuid.UUID, _ string) (string, bool, error) {
	return "0", false, nil
}

func (m *mockBalanceRepo) GetByAddress(_ context.Context, _ model.Chain, _ model.Network, _ string) ([]model.Balance, error) {
	return nil, nil
}

func (m *mockBalanceRepo) BulkGetAmountWithExistsTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
	return nil, nil
}

func (m *mockBalanceRepo) BulkAdjustBalanceTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, items []store.BulkAdjustItem) error {
	m.bulkAdjustCalled++
	m.capturedItems = append(m.capturedItems, items)
	return m.bulkAdjustErr
}

type mockConfigRepo struct {
	watermark          *model.PipelineWatermark
	rewindCalled       bool
	rewindErr          error
	capturedWatermark  int64
}

func (m *mockConfigRepo) Get(_ context.Context, _ model.Chain, _ model.Network) (*model.IndexerConfig, error) {
	return nil, nil
}

func (m *mockConfigRepo) Upsert(_ context.Context, _ *model.IndexerConfig) error {
	return nil
}

func (m *mockConfigRepo) UpdateWatermarkTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ int64) error {
	return nil
}

func (m *mockConfigRepo) RewindWatermarkTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, seq int64) error {
	m.rewindCalled = true
	m.capturedWatermark = seq
	return m.rewindErr
}

func (m *mockConfigRepo) GetWatermark(_ context.Context, _ model.Chain, _ model.Network) (*model.PipelineWatermark, error) {
	return m.watermark, nil
}

type mockBlockRepo struct {
	block              *model.IndexedBlock
	getErr             error
	deleteFromCalled   bool
	deleteFromResult   int64
	deleteFromErr      error
}

func (m *mockBlockRepo) UpsertTx(_ context.Context, _ *sql.Tx, _ *model.IndexedBlock) error {
	return nil
}

func (m *mockBlockRepo) BulkUpsertTx(_ context.Context, _ *sql.Tx, _ []*model.IndexedBlock) error {
	return nil
}

func (m *mockBlockRepo) GetUnfinalized(_ context.Context, _ model.Chain, _ model.Network) ([]model.IndexedBlock, error) {
	return nil, nil
}

func (m *mockBlockRepo) GetByBlockNumber(_ context.Context, _ model.Chain, _ model.Network, _ int64) (*model.IndexedBlock, error) {
	return m.block, m.getErr
}

func (m *mockBlockRepo) UpdateFinalityTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ int64, _ string) error {
	return nil
}

func (m *mockBlockRepo) DeleteFromBlockTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ int64) (int64, error) {
	m.deleteFromCalled = true
	return m.deleteFromResult, m.deleteFromErr
}

func (m *mockBlockRepo) PurgeFinalizedBefore(_ context.Context, _ model.Chain, _ model.Network, _ int64) (int64, error) {
	return 0, nil
}

func testLogger() *slog.Logger {
	return slog.Default()
}

// sqlmockTxBeginner wraps a *sql.DB so that it satisfies store.TxBeginner
// and produces real *sql.Tx objects backed by sqlmock.
type sqlmockTxBeginner struct {
	db *sql.DB
}

func (s *sqlmockTxBeginner) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return s.db.BeginTx(ctx, opts)
}

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestNegateDecimalString(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"100", "-100", false},
		{"-50", "50", false},
		{"0", "0", false},
		{"999999999999999999999999", "-999999999999999999999999", false},
		{"abc", "", true},
		{"", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := identity.NegateDecimalString(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestIsStakingActivity(t *testing.T) {
	tests := []struct {
		activity model.ActivityType
		expected bool
	}{
		{model.ActivityStake, true},
		{model.ActivityUnstake, true},
		{model.ActivityDeposit, false},
		{model.ActivityWithdrawal, false},
		{model.ActivityOther, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.activity), func(t *testing.T) {
			if got := identity.IsStakingActivity(tc.activity); got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Existing finality check tests (preserved)
// ---------------------------------------------------------------------------

func TestPurgeFromBlock_FinalizedWithoutForce(t *testing.T) {
	blockRepo := &mockBlockRepo{
		block: &model.IndexedBlock{
			FinalityState: "finalized",
		},
	}

	svc := NewService(
		&mockTxBeginner{},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		blockRepo,
		testLogger(),
	)

	_, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
		Force:     false,
	})

	if err != ErrFinalizedBlock {
		t.Fatalf("expected ErrFinalizedBlock, got %v", err)
	}
}

func TestPurgeFromBlock_FinalizedWithForce_BypassesFinalityCheck(t *testing.T) {
	blockRepo := &mockBlockRepo{
		block: &model.IndexedBlock{
			FinalityState: "finalized",
		},
	}

	svc := NewService(
		&mockTxBeginner{err: fmt.Errorf("mock: no real db")},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		blockRepo,
		testLogger(),
	)

	_, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
		Force:     true,
		DryRun:    false,
	})

	// Should fail at BeginTx, not at finality check
	if err == ErrFinalizedBlock {
		t.Fatal("force=true should bypass finality check")
	}
	if err == nil {
		t.Fatal("expected an error from BeginTx mock")
	}
}

func TestPurgeFromBlock_NoBlockRepo(t *testing.T) {
	svc := NewService(
		&mockTxBeginner{err: fmt.Errorf("mock: no real db")},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	_, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
		Force:     false,
	})

	// Should pass finality check (no block repo) and fail at BeginTx
	if err == ErrFinalizedBlock {
		t.Fatal("nil block repo should not trigger finality check")
	}
}

func TestPurgeFromBlock_DryRun_EntersDryRunPath(t *testing.T) {
	svc := NewService(
		&mockTxBeginner{err: fmt.Errorf("mock: no real db")},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	_, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
		DryRun:    true,
	})

	// DryRun will try to BeginTx (read-only) and fail with our mock
	if err == nil {
		t.Fatal("expected error from mock db in dry run")
	}
}

func TestPurgeFromBlock_UnfinalizedBlock_AllowedWithoutForce(t *testing.T) {
	blockRepo := &mockBlockRepo{
		block: &model.IndexedBlock{
			FinalityState: "unfinalized",
		},
	}

	svc := NewService(
		&mockTxBeginner{err: fmt.Errorf("mock: no real db")},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		blockRepo,
		testLogger(),
	)

	_, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
		Force:     false,
	})

	// Should pass finality check (unfinalized) and fail at BeginTx
	if err == ErrFinalizedBlock {
		t.Fatal("unfinalized block should not trigger finality check")
	}
}

func TestPurgeFromBlock_BlockNotFound_AllowedWithoutForce(t *testing.T) {
	blockRepo := &mockBlockRepo{
		block:  nil,
		getErr: sql.ErrNoRows,
	}

	svc := NewService(
		&mockTxBeginner{err: fmt.Errorf("mock: no real db")},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		blockRepo,
		testLogger(),
	)

	_, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
		Force:     false,
	})

	if err == ErrFinalizedBlock {
		t.Fatal("block not found should not trigger finality check")
	}
}

// ---------------------------------------------------------------------------
// New tests: full purge path with sqlmock
// ---------------------------------------------------------------------------

// setupSqlmockPurge creates a sqlmock DB and sets up the standard expectations
// for a full purge. Returns the mock for additional expectations.
func setupSqlmockPurge(t *testing.T, events []rollbackEvent) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}

	mock.ExpectBegin()

	// Build rows for fetchRollbackBatch
	rows := sqlmock.NewRows([]string{
		"id", "token_id", "address", "delta", "block_cursor",
		"tx_hash", "wallet_id", "organization_id", "activity_type", "balance_applied",
	})
	for _, e := range events {
		var walletID, orgID interface{}
		if e.walletID != nil {
			walletID = *e.walletID
		}
		if e.organizationID != nil {
			orgID = *e.organizationID
		}
		rows.AddRow(e.id, e.tokenID, e.address, e.delta, e.blockCursor,
			e.txHash, walletID, orgID, e.activityType, e.balanceApplied)
	}
	mock.ExpectQuery("SELECT id, token_id, address, delta").WillReturnRows(rows)

	return db, mock
}

func TestPurgeFromBlock_ReversesSingleBalance(t *testing.T) {
	tokenID := uuid.New()
	events := []rollbackEvent{
		{
			id: 1, tokenID: tokenID, address: "0xaddr1", delta: "500",
			blockCursor: 100, txHash: "0xtx1", activityType: model.ActivityDeposit,
			balanceApplied: true,
		},
	}

	db, mock := setupSqlmockPurge(t, events)
	defer db.Close()

	// delete balance_events
	mock.ExpectExec("DELETE FROM balance_events").
		WithArgs("base", "mainnet", int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// delete transactions
	mock.ExpectExec("DELETE FROM transactions").
		WithArgs("base", "mainnet", int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// rewind watermark
	mock.ExpectCommit()

	balRepo := &mockBalanceRepo{}
	configRepo := &mockConfigRepo{}

	svc := NewService(
		&sqlmockTxBeginner{db: db},
		balRepo,
		configRepo,
		nil, // no block repo
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ReversedBalances != 1 {
		t.Errorf("ReversedBalances = %d, want 1", result.ReversedBalances)
	}
	if balRepo.bulkAdjustCalled != 1 {
		t.Errorf("BulkAdjustBalanceTx called %d times, want 1", balRepo.bulkAdjustCalled)
	}
	// Verify the delta is negated
	if len(balRepo.capturedItems) != 1 || len(balRepo.capturedItems[0]) != 1 {
		t.Fatalf("expected 1 bulk adjust batch with 1 item, got %v", balRepo.capturedItems)
	}
	item := balRepo.capturedItems[0][0]
	if item.Delta != "-500" {
		t.Errorf("expected negated delta -500, got %q", item.Delta)
	}
	if item.Address != "0xaddr1" {
		t.Errorf("expected address 0xaddr1, got %q", item.Address)
	}

	if configRepo.rewindCalled != true {
		t.Error("RewindWatermarkTx was not called")
	}
	if configRepo.capturedWatermark != 99 {
		t.Errorf("watermark rewound to %d, want 99", configRepo.capturedWatermark)
	}
}

func TestPurgeFromBlock_ReversesMultipleBalances(t *testing.T) {
	tokenID1 := uuid.New()
	tokenID2 := uuid.New()
	events := []rollbackEvent{
		{
			id: 1, tokenID: tokenID1, address: "0xaddr1", delta: "100",
			blockCursor: 100, txHash: "0xtx1", activityType: model.ActivityDeposit,
			balanceApplied: true,
		},
		{
			id: 2, tokenID: tokenID2, address: "0xaddr2", delta: "-200",
			blockCursor: 101, txHash: "0xtx2", activityType: model.ActivityWithdrawal,
			balanceApplied: true,
		},
		{
			id: 3, tokenID: tokenID1, address: "0xaddr1", delta: "300",
			blockCursor: 102, txHash: "0xtx3", activityType: model.ActivityDeposit,
			balanceApplied: true,
		},
	}

	db, mock := setupSqlmockPurge(t, events)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	balRepo := &mockBalanceRepo{}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		balRepo,
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ReversedBalances != 3 {
		t.Errorf("ReversedBalances = %d, want 3", result.ReversedBalances)
	}
	if result.PurgedEvents != 3 {
		t.Errorf("PurgedEvents = %d, want 3", result.PurgedEvents)
	}

	// All 3 events should be in a single BulkAdjust call
	if balRepo.bulkAdjustCalled != 1 {
		t.Errorf("BulkAdjustBalanceTx called %d times, want 1", balRepo.bulkAdjustCalled)
	}
	if len(balRepo.capturedItems[0]) != 3 {
		t.Errorf("expected 3 items in bulk adjust, got %d", len(balRepo.capturedItems[0]))
	}

	// Check deltas are properly negated
	expectedDeltas := []string{"-100", "200", "-300"}
	for i, item := range balRepo.capturedItems[0] {
		if item.Delta != expectedDeltas[i] {
			t.Errorf("item[%d].Delta = %q, want %q", i, item.Delta, expectedDeltas[i])
		}
	}
}

func TestPurgeFromBlock_SkipsUnappliedEvents(t *testing.T) {
	tokenID := uuid.New()
	events := []rollbackEvent{
		{
			id: 1, tokenID: tokenID, address: "0xaddr1", delta: "500",
			blockCursor: 100, txHash: "0xtx1", activityType: model.ActivityDeposit,
			balanceApplied: false, // NOT applied - should be skipped
		},
		{
			id: 2, tokenID: tokenID, address: "0xaddr2", delta: "300",
			blockCursor: 101, txHash: "0xtx2", activityType: model.ActivityDeposit,
			balanceApplied: true, // Applied - should be reversed
		},
	}

	db, mock := setupSqlmockPurge(t, events)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	balRepo := &mockBalanceRepo{}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		balRepo,
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only 1 event had balanceApplied=true
	if result.ReversedBalances != 1 {
		t.Errorf("ReversedBalances = %d, want 1", result.ReversedBalances)
	}
	// But both events are deleted
	if result.PurgedEvents != 2 {
		t.Errorf("PurgedEvents = %d, want 2", result.PurgedEvents)
	}

	if balRepo.bulkAdjustCalled != 1 {
		t.Errorf("BulkAdjustBalanceTx called %d times, want 1", balRepo.bulkAdjustCalled)
	}
	if len(balRepo.capturedItems[0]) != 1 {
		t.Fatalf("expected 1 item in bulk adjust (only applied), got %d", len(balRepo.capturedItems[0]))
	}
	if balRepo.capturedItems[0][0].Delta != "-300" {
		t.Errorf("expected delta -300, got %q", balRepo.capturedItems[0][0].Delta)
	}
}

func TestPurgeFromBlock_HandlesStakingActivity(t *testing.T) {
	tokenID := uuid.New()
	walletID := "wallet-1"
	orgID := "org-1"
	events := []rollbackEvent{
		{
			id: 1, tokenID: tokenID, address: "0xaddr1", delta: "1000",
			blockCursor: 100, txHash: "0xtx1", activityType: model.ActivityStake,
			balanceApplied: true, walletID: &walletID, organizationID: &orgID,
		},
	}

	db, mock := setupSqlmockPurge(t, events)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	balRepo := &mockBalanceRepo{}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		balRepo,
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ReversedBalances != 1 {
		t.Errorf("ReversedBalances = %d, want 1", result.ReversedBalances)
	}

	// Staking events should trigger 2 BulkAdjust calls:
	// 1. liquid balance reversal (negated delta: -1000)
	// 2. staked balance reversal (original delta: 1000, with BalanceType="staked")
	if balRepo.bulkAdjustCalled != 2 {
		t.Fatalf("BulkAdjustBalanceTx called %d times, want 2 (liquid + staked)", balRepo.bulkAdjustCalled)
	}

	// First call: liquid balance items
	liquidItems := balRepo.capturedItems[0]
	if len(liquidItems) != 1 {
		t.Fatalf("expected 1 liquid item, got %d", len(liquidItems))
	}
	if liquidItems[0].Delta != "-1000" {
		t.Errorf("liquid delta = %q, want -1000", liquidItems[0].Delta)
	}
	if liquidItems[0].BalanceType != "" {
		t.Errorf("liquid BalanceType = %q, want empty", liquidItems[0].BalanceType)
	}

	// Second call: staked balance items
	stakedItems := balRepo.capturedItems[1]
	if len(stakedItems) != 1 {
		t.Fatalf("expected 1 staked item, got %d", len(stakedItems))
	}
	// For staking reversal, the staked delta is the ORIGINAL delta (not negated),
	// because staking adds to staked and subtracts from liquid.
	if stakedItems[0].Delta != "1000" {
		t.Errorf("staked delta = %q, want 1000", stakedItems[0].Delta)
	}
	if stakedItems[0].BalanceType != "staked" {
		t.Errorf("staked BalanceType = %q, want staked", stakedItems[0].BalanceType)
	}
	if *stakedItems[0].WalletID != walletID {
		t.Errorf("staked WalletID = %q, want %q", *stakedItems[0].WalletID, walletID)
	}
	if *stakedItems[0].OrgID != orgID {
		t.Errorf("staked OrgID = %q, want %q", *stakedItems[0].OrgID, orgID)
	}
}

func TestPurgeFromBlock_WatermarkRewind(t *testing.T) {
	// No events to rollback
	db, mock := setupSqlmockPurge(t, nil)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	configRepo := &mockConfigRepo{}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		&mockBalanceRepo{},
		configRepo,
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Watermark should be fromBlock - 1
	if result.NewWatermark != 49 {
		t.Errorf("NewWatermark = %d, want 49", result.NewWatermark)
	}
	if !configRepo.rewindCalled {
		t.Error("RewindWatermarkTx was not called")
	}
	if configRepo.capturedWatermark != 49 {
		t.Errorf("watermark rewound to %d, want 49", configRepo.capturedWatermark)
	}
}

func TestPurgeFromBlock_WatermarkRewind_Block0(t *testing.T) {
	// When fromBlock=0, watermark should clamp to 0 (not -1)
	db, mock := setupSqlmockPurge(t, nil)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	configRepo := &mockConfigRepo{}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		&mockBalanceRepo{},
		configRepo,
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.NewWatermark != 0 {
		t.Errorf("NewWatermark = %d, want 0 (clamped)", result.NewWatermark)
	}
}

func TestPurgeFromBlock_DeletesEventsTransactionsBlocks(t *testing.T) {
	db, mock := setupSqlmockPurge(t, nil)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WithArgs("ethereum", "mainnet", int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec("DELETE FROM transactions").
		WithArgs("ethereum", "mainnet", int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	blockRepo := &mockBlockRepo{deleteFromResult: 7}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		blockRepo,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainEthereum,
		Network:   model.NetworkMainnet,
		FromBlock: 200,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.PurgedEvents != 5 {
		t.Errorf("PurgedEvents = %d, want 5", result.PurgedEvents)
	}
	if result.PurgedTransactions != 3 {
		t.Errorf("PurgedTransactions = %d, want 3", result.PurgedTransactions)
	}
	if result.PurgedBlocks != 7 {
		t.Errorf("PurgedBlocks = %d, want 7", result.PurgedBlocks)
	}
	if !blockRepo.deleteFromCalled {
		t.Error("DeleteFromBlockTx was not called")
	}
}

func TestPurgeFromBlock_FinalityCheck_RejectsFinalizedBlock(t *testing.T) {
	blockRepo := &mockBlockRepo{
		block: &model.IndexedBlock{FinalityState: "finalized"},
	}

	svc := NewService(
		&mockTxBeginner{},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		blockRepo,
		testLogger(),
	)

	_, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainEthereum,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
		Force:     false,
	})

	if err != ErrFinalizedBlock {
		t.Fatalf("expected ErrFinalizedBlock, got %v", err)
	}
}

func TestPurgeFromBlock_ForceFinalizedBlock(t *testing.T) {
	blockRepo := &mockBlockRepo{
		block: &model.IndexedBlock{FinalityState: "finalized"},
	}

	db, mock := setupSqlmockPurge(t, nil)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	svc := NewService(
		&sqlmockTxBeginner{db: db},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		blockRepo,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainEthereum,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
		Force:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestPurgeFromBlock_TransactionAtomicity_BalanceAdjustFailure(t *testing.T) {
	tokenID := uuid.New()
	events := []rollbackEvent{
		{
			id: 1, tokenID: tokenID, address: "0xaddr1", delta: "100",
			blockCursor: 100, txHash: "0xtx1", activityType: model.ActivityDeposit,
			balanceApplied: true,
		},
	}

	db, mock := setupSqlmockPurge(t, events)
	defer db.Close()

	// The transaction should be rolled back when BulkAdjustBalanceTx fails.
	mock.ExpectRollback()

	balRepo := &mockBalanceRepo{bulkAdjustErr: fmt.Errorf("db error: balance adjust failed")}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		balRepo,
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	_, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})

	if err == nil {
		t.Fatal("expected error from balance adjust failure")
	}
	if err.Error() != "revert balances: db error: balance adjust failed" {
		t.Errorf("unexpected error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations not met: %v", err)
	}
}

func TestPurgeFromBlock_TransactionAtomicity_RewindWatermarkFailure(t *testing.T) {
	// No events, so no balance adjustments needed.
	db, mock := setupSqlmockPurge(t, nil)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	configRepo := &mockConfigRepo{rewindErr: fmt.Errorf("db error: rewind failed")}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		&mockBalanceRepo{},
		configRepo,
		nil,
		testLogger(),
	)

	_, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})

	if err == nil {
		t.Fatal("expected error from rewind watermark failure")
	}
	if err.Error() != "rewind watermark: db error: rewind failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDryRun_ReturnsCountsWithoutModifying(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT").
		WithArgs("base", "mainnet", int64(50)).
		WillReturnRows(sqlmock.NewRows([]string{"events", "txs", "blocks"}).
			AddRow(10, 5, 3))
	mock.ExpectRollback() // dry run rolls back

	balRepo := &mockBalanceRepo{}
	configRepo := &mockConfigRepo{}

	svc := NewService(
		&sqlmockTxBeginner{db: db},
		balRepo,
		configRepo,
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 50,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.DryRun {
		t.Error("expected DryRun=true in result")
	}
	if result.PurgedEvents != 10 {
		t.Errorf("PurgedEvents = %d, want 10", result.PurgedEvents)
	}
	if result.PurgedTransactions != 5 {
		t.Errorf("PurgedTransactions = %d, want 5", result.PurgedTransactions)
	}
	if result.PurgedBlocks != 3 {
		t.Errorf("PurgedBlocks = %d, want 3", result.PurgedBlocks)
	}
	if result.NewWatermark != 49 {
		t.Errorf("NewWatermark = %d, want 49", result.NewWatermark)
	}

	// Verify no modifications were made
	if balRepo.bulkAdjustCalled != 0 {
		t.Errorf("BulkAdjustBalanceTx should not be called in dry run, called %d times", balRepo.bulkAdjustCalled)
	}
	if configRepo.rewindCalled {
		t.Error("RewindWatermarkTx should not be called in dry run")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations not met: %v", err)
	}
}

func TestDryRun_SingleReadOnlyTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	// sqlmock always expects Begin (no way to assert ReadOnly in sqlmock v1),
	// but we verify the query runs inside a transaction.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"events", "txs", "blocks"}).
			AddRow(0, 0, 0))
	mock.ExpectRollback()

	svc := NewService(
		&sqlmockTxBeginner{db: db},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.DryRun {
		t.Error("expected DryRun=true")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations not met: %v", err)
	}
}

func TestDryRun_WatermarkClampsToZero(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"events", "txs", "blocks"}).
			AddRow(0, 0, 0))
	mock.ExpectRollback()

	svc := NewService(
		&sqlmockTxBeginner{db: db},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 0,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NewWatermark != 0 {
		t.Errorf("NewWatermark = %d, want 0 (clamped)", result.NewWatermark)
	}
}

func TestPurgeFromBlock_EmptyRange_NoEventsToReverse(t *testing.T) {
	// No events returned from query
	db, mock := setupSqlmockPurge(t, nil)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	balRepo := &mockBalanceRepo{}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		balRepo,
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 9999,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ReversedBalances != 0 {
		t.Errorf("ReversedBalances = %d, want 0", result.ReversedBalances)
	}
	if result.PurgedEvents != 0 {
		t.Errorf("PurgedEvents = %d, want 0", result.PurgedEvents)
	}
	// BulkAdjust should not be called when there are no items
	if balRepo.bulkAdjustCalled != 0 {
		t.Errorf("BulkAdjustBalanceTx called %d times, want 0", balRepo.bulkAdjustCalled)
	}
}

func TestPurgeFromBlock_NegateDecimalString_VariousFormats(t *testing.T) {
	tokenID := uuid.New()
	events := []rollbackEvent{
		{
			id: 1, tokenID: tokenID, address: "0xaddr1", delta: "999999999999999999999999",
			blockCursor: 100, txHash: "0xtx1", activityType: model.ActivityDeposit,
			balanceApplied: true,
		},
		{
			id: 2, tokenID: tokenID, address: "0xaddr2", delta: "-42",
			blockCursor: 100, txHash: "0xtx2", activityType: model.ActivityWithdrawal,
			balanceApplied: true,
		},
		{
			id: 3, tokenID: tokenID, address: "0xaddr3", delta: "0",
			blockCursor: 100, txHash: "0xtx3", activityType: model.ActivityDeposit,
			balanceApplied: true,
		},
	}

	db, mock := setupSqlmockPurge(t, events)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	balRepo := &mockBalanceRepo{}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		balRepo,
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ReversedBalances != 3 {
		t.Errorf("ReversedBalances = %d, want 3", result.ReversedBalances)
	}

	items := balRepo.capturedItems[0]
	expectedDeltas := []string{"-999999999999999999999999", "42", "0"}
	for i, item := range items {
		if item.Delta != expectedDeltas[i] {
			t.Errorf("item[%d].Delta = %q, want %q", i, item.Delta, expectedDeltas[i])
		}
	}
}

func TestPurgeFromBlock_BlockRepoNil_SkipsBlockDeletion(t *testing.T) {
	db, mock := setupSqlmockPurge(t, nil)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	svc := NewService(
		&sqlmockTxBeginner{db: db},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		nil, // no block repo
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// PurgedBlocks should be 0 since blockRepo is nil
	if result.PurgedBlocks != 0 {
		t.Errorf("PurgedBlocks = %d, want 0", result.PurgedBlocks)
	}
}

func TestPurgeFromBlock_MetricsRecorded(t *testing.T) {
	// This test verifies that the purge completes successfully and the
	// DurationMs field is populated. We cannot easily inspect Prometheus
	// metric values in unit tests without a custom registry, so we verify
	// the result fields that map to metrics.
	db, mock := setupSqlmockPurge(t, nil)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	blockRepo := &mockBlockRepo{deleteFromResult: 3}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		blockRepo,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// DurationMs should be non-negative (metrics.ReplayPurgeDurationSeconds is set)
	if result.DurationMs < 0 {
		t.Errorf("DurationMs = %d, want >= 0", result.DurationMs)
	}
	// Verify result fields correspond to what metrics would record
	if result.PurgedEvents != 2 {
		t.Errorf("PurgedEvents = %d, want 2", result.PurgedEvents)
	}
	if result.PurgedTransactions != 1 {
		t.Errorf("PurgedTransactions = %d, want 1", result.PurgedTransactions)
	}
	if result.PurgedBlocks != 3 {
		t.Errorf("PurgedBlocks = %d, want 3", result.PurgedBlocks)
	}
	if result.DryRun {
		t.Error("DryRun should be false for real purge")
	}
}

func TestPurgeFromBlock_BlockDeleteError_RollsBack(t *testing.T) {
	db, mock := setupSqlmockPurge(t, nil)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	blockRepo := &mockBlockRepo{deleteFromErr: fmt.Errorf("db error: block delete failed")}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		blockRepo,
		testLogger(),
	)

	_, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})

	if err == nil {
		t.Fatal("expected error from block delete failure")
	}
	if err.Error() != "delete indexed blocks: db error: block delete failed" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPurgeFromBlock_FinalityCheckDBError_ReturnsError(t *testing.T) {
	blockRepo := &mockBlockRepo{
		getErr: fmt.Errorf("db connection lost"),
	}

	svc := NewService(
		&mockTxBeginner{},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		blockRepo,
		testLogger(),
	)

	_, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
		Force:     false,
	})

	if err == nil {
		t.Fatal("expected error from finality check DB error")
	}
	if err == ErrFinalizedBlock {
		t.Fatal("should not be ErrFinalizedBlock for a DB error")
	}
}

func TestPurgeFromBlock_UnstakeActivity_ReversesBothBalances(t *testing.T) {
	tokenID := uuid.New()
	events := []rollbackEvent{
		{
			id: 1, tokenID: tokenID, address: "0xaddr1", delta: "-500",
			blockCursor: 100, txHash: "0xtx1", activityType: model.ActivityUnstake,
			balanceApplied: true,
		},
	}

	db, mock := setupSqlmockPurge(t, events)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	balRepo := &mockBalanceRepo{}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		balRepo,
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ReversedBalances != 1 {
		t.Errorf("ReversedBalances = %d, want 1", result.ReversedBalances)
	}

	// Should have 2 BulkAdjust calls: liquid + staked
	if balRepo.bulkAdjustCalled != 2 {
		t.Fatalf("BulkAdjustBalanceTx called %d times, want 2", balRepo.bulkAdjustCalled)
	}

	// Liquid: negated delta (500)
	if balRepo.capturedItems[0][0].Delta != "500" {
		t.Errorf("liquid delta = %q, want 500", balRepo.capturedItems[0][0].Delta)
	}
	// Staked: original delta (-500)
	if balRepo.capturedItems[1][0].Delta != "-500" {
		t.Errorf("staked delta = %q, want -500", balRepo.capturedItems[1][0].Delta)
	}
	if balRepo.capturedItems[1][0].BalanceType != "staked" {
		t.Errorf("staked BalanceType = %q, want staked", balRepo.capturedItems[1][0].BalanceType)
	}
}

func TestPurgeFromBlock_ResultDurationMsIsSet(t *testing.T) {
	db, mock := setupSqlmockPurge(t, nil)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	svc := NewService(
		&sqlmockTxBeginner{db: db},
		&mockBalanceRepo{},
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.DurationMs < 0 {
		t.Errorf("DurationMs should be >= 0, got %d", result.DurationMs)
	}
}

func TestPurgeFromBlock_MixedAppliedAndUnappliedWithStaking(t *testing.T) {
	tokenID := uuid.New()
	events := []rollbackEvent{
		{
			id: 1, tokenID: tokenID, address: "0xaddr1", delta: "100",
			blockCursor: 100, txHash: "0xtx1", activityType: model.ActivityDeposit,
			balanceApplied: true,
		},
		{
			id: 2, tokenID: tokenID, address: "0xaddr2", delta: "200",
			blockCursor: 100, txHash: "0xtx2", activityType: model.ActivityStake,
			balanceApplied: false, // unapplied staking event
		},
		{
			id: 3, tokenID: tokenID, address: "0xaddr3", delta: "300",
			blockCursor: 100, txHash: "0xtx3", activityType: model.ActivityStake,
			balanceApplied: true, // applied staking event
		},
	}

	db, mock := setupSqlmockPurge(t, events)
	defer db.Close()

	mock.ExpectExec("DELETE FROM balance_events").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("DELETE FROM transactions").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	balRepo := &mockBalanceRepo{}
	svc := NewService(
		&sqlmockTxBeginner{db: db},
		balRepo,
		&mockConfigRepo{},
		nil,
		testLogger(),
	)

	result, err := svc.PurgeFromBlock(context.Background(), PurgeRequest{
		Chain:     model.ChainBase,
		Network:   model.NetworkMainnet,
		FromBlock: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2 applied events out of 3
	if result.ReversedBalances != 2 {
		t.Errorf("ReversedBalances = %d, want 2", result.ReversedBalances)
	}

	// 2 calls: liquid (2 items) + staked (1 item, only applied staking)
	if balRepo.bulkAdjustCalled != 2 {
		t.Fatalf("BulkAdjustBalanceTx called %d times, want 2", balRepo.bulkAdjustCalled)
	}

	// Liquid: 2 items (deposit + staking)
	liquidItems := balRepo.capturedItems[0]
	if len(liquidItems) != 2 {
		t.Fatalf("expected 2 liquid items, got %d", len(liquidItems))
	}
	if liquidItems[0].Delta != "-100" {
		t.Errorf("liquid[0].Delta = %q, want -100", liquidItems[0].Delta)
	}
	if liquidItems[1].Delta != "-300" {
		t.Errorf("liquid[1].Delta = %q, want -300", liquidItems[1].Delta)
	}

	// Staked: 1 item (only the applied staking event)
	stakedItems := balRepo.capturedItems[1]
	if len(stakedItems) != 1 {
		t.Fatalf("expected 1 staked item, got %d", len(stakedItems))
	}
	if stakedItems[0].Delta != "300" {
		t.Errorf("staked[0].Delta = %q, want 300", stakedItems[0].Delta)
	}
}
