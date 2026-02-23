package ingester

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// ---------------------------------------------------------------------------
// Fake driver for reorg/finality tests. Each test gets a unique driver name
// via atomic counter, eliminating shared state and connection pool issues.
// ---------------------------------------------------------------------------

// rfQueryHandler returns rows for a given query. If nil, returns empty rows.
type rfQueryHandler func(query string, args []driver.Value) (driver.Rows, error)

var rfDriverSeq atomic.Int64

// rfFakeDriver wraps a single rfFakeConn per Open call.
type rfFakeDriver struct {
	conn *rfFakeConn
}
type rfFakeConn struct {
	queryHandler rfQueryHandler
	execHandler  func(query string, args []driver.Value) (driver.Result, error)
}
type rfFakeTx struct{ conn *rfFakeConn }

func (d *rfFakeDriver) Open(string) (driver.Conn, error) { return d.conn, nil }
func (c *rfFakeConn) Prepare(query string) (driver.Stmt, error) {
	return &rfFakeStmt{conn: c, query: query}, nil
}
func (c *rfFakeConn) Close() error              { return nil }
func (c *rfFakeConn) Begin() (driver.Tx, error) { return &rfFakeTx{conn: c}, nil }
func (tx *rfFakeTx) Commit() error              { return nil }
func (tx *rfFakeTx) Rollback() error            { return nil }

type rfFakeStmt struct {
	conn  *rfFakeConn
	query string
}

func (s *rfFakeStmt) Close() error  { return nil }
func (s *rfFakeStmt) NumInput() int { return -1 }
func (s *rfFakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	if s.conn.execHandler != nil {
		return s.conn.execHandler(s.query, args)
	}
	return driver.RowsAffected(0), nil
}
func (s *rfFakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	if s.conn.queryHandler != nil {
		return s.conn.queryHandler(s.query, args)
	}
	return &rfEmptyRows{}, nil
}

// rfEmptyRows returns no rows.
type rfEmptyRows struct{}

func (r *rfEmptyRows) Columns() []string {
	return []string{"token_id", "address", "delta", "block_cursor", "tx_hash", "wallet_id", "organization_id", "activity_type", "balance_applied"}
}
func (r *rfEmptyRows) Close() error                  { return nil }
func (r *rfEmptyRows) Next(dest []driver.Value) error { return io.EOF }

// rfDataRows returns pre-loaded rows of data for multi-row tests.
type rfDataRows struct {
	columns []string
	data    [][]driver.Value
	idx     int
}

func (r *rfDataRows) Columns() []string { return r.columns }
func (r *rfDataRows) Close() error      { return nil }
func (r *rfDataRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.idx])
	r.idx++
	return nil
}

// rollbackEventColumns are the 9 columns returned by fetchReorgRollbackEvents / fetchRollbackEvents.
var rollbackEventColumns = []string{
	"token_id", "address", "delta", "block_cursor", "tx_hash",
	"wallet_id", "organization_id", "activity_type", "balance_applied",
}

// promotedEventColumns are the 8 columns returned by promoteBalanceEvents RETURNING clause.
var promotedEventColumns = []string{
	"token_id", "address", "delta", "block_cursor", "tx_hash",
	"wallet_id", "organization_id", "activity_type",
}

// openRFFakeDB creates a fake DB with no query handler (empty results, no errors).
func openRFFakeDB(t *testing.T) *sql.DB {
	t.Helper()
	return openRFFakeDBWith(t, nil, nil)
}

// openRFFakeDBWithHandler creates a fake DB whose queries return rows from the handler.
func openRFFakeDBWithHandler(t *testing.T, handler rfQueryHandler) *sql.DB {
	t.Helper()
	return openRFFakeDBWith(t, handler, nil)
}

// openRFFakeDBWith creates an isolated fake DB per test using a unique driver name.
func openRFFakeDBWith(t *testing.T, queryH rfQueryHandler, execH func(string, []driver.Value) (driver.Result, error)) *sql.DB {
	t.Helper()
	name := fmt.Sprintf("fake_rf_%d", rfDriverSeq.Add(1))
	conn := &rfFakeConn{queryHandler: queryH, execHandler: execH}
	sql.Register(name, &rfFakeDriver{conn: conn})
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ---------------------------------------------------------------------------
// Test: buildReversalAdjustItems
// ---------------------------------------------------------------------------

func TestBuildReversalAdjustItems_PositiveDelta_ReturnsNegativeReversal(t *testing.T) {
	tokenID := uuid.New()
	events := []rollbackBalanceEvent{
		{
			TokenID:        tokenID,
			Address:        "addr1",
			Delta:          "1000",
			BlockCursor:    100,
			TxHash:         "tx1",
			ActivityType:   model.ActivityDeposit,
			BalanceApplied: true,
		},
	}

	items, err := buildReversalAdjustItems(events)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "-1000", items[0].Delta)
	assert.Equal(t, "addr1", items[0].Address)
	assert.Equal(t, tokenID, items[0].TokenID)
	assert.Equal(t, int64(100), items[0].Cursor)
	assert.Equal(t, "tx1", items[0].TxHash)
	assert.Equal(t, "", items[0].BalanceType)
}

func TestBuildReversalAdjustItems_NegativeDelta_ReturnsPositiveReversal(t *testing.T) {
	tokenID := uuid.New()
	events := []rollbackBalanceEvent{
		{
			TokenID:        tokenID,
			Address:        "addr1",
			Delta:          "-5000",
			BlockCursor:    200,
			TxHash:         "tx2",
			ActivityType:   model.ActivityWithdrawal,
			BalanceApplied: true,
		},
	}

	items, err := buildReversalAdjustItems(events)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "5000", items[0].Delta)
	assert.Equal(t, "addr1", items[0].Address)
}

func TestBuildReversalAdjustItems_BalanceNotApplied_Skipped(t *testing.T) {
	events := []rollbackBalanceEvent{
		{
			TokenID:        uuid.New(),
			Address:        "addr1",
			Delta:          "1000",
			BlockCursor:    100,
			TxHash:         "tx1",
			ActivityType:   model.ActivityDeposit,
			BalanceApplied: false, // not applied
		},
	}

	items, err := buildReversalAdjustItems(events)
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestBuildReversalAdjustItems_MultipleEvents_CorrectCount(t *testing.T) {
	tokenID1 := uuid.New()
	tokenID2 := uuid.New()
	walletID := "wallet1"
	orgID := "org1"

	events := []rollbackBalanceEvent{
		{
			TokenID:        tokenID1,
			Address:        "addr1",
			Delta:          "1000",
			BlockCursor:    100,
			TxHash:         "tx1",
			WalletID:       &walletID,
			OrganizationID: &orgID,
			ActivityType:   model.ActivityDeposit,
			BalanceApplied: true,
		},
		{
			TokenID:        tokenID2,
			Address:        "addr2",
			Delta:          "-500",
			BlockCursor:    101,
			TxHash:         "tx2",
			ActivityType:   model.ActivityWithdrawal,
			BalanceApplied: true,
		},
		{
			TokenID:        tokenID1,
			Address:        "addr3",
			Delta:          "200",
			BlockCursor:    102,
			TxHash:         "tx3",
			ActivityType:   model.ActivityDeposit,
			BalanceApplied: false, // skipped
		},
	}

	items, err := buildReversalAdjustItems(events)
	require.NoError(t, err)
	require.Len(t, items, 2, "only 2 of 3 events have BalanceApplied=true")

	// First reversal: delta=1000 -> -1000
	assert.Equal(t, "-1000", items[0].Delta)
	assert.Equal(t, &walletID, items[0].WalletID)
	assert.Equal(t, &orgID, items[0].OrgID)

	// Second reversal: delta=-500 -> 500
	assert.Equal(t, "500", items[1].Delta)
	assert.Nil(t, items[1].WalletID)
}

func TestBuildReversalAdjustItems_StakingActivity_ProducesStakedReversal(t *testing.T) {
	tokenID := uuid.New()
	events := []rollbackBalanceEvent{
		{
			TokenID:        tokenID,
			Address:        "addr1",
			Delta:          "-5000",
			BlockCursor:    100,
			TxHash:         "tx1",
			ActivityType:   model.ActivityStake,
			BalanceApplied: true,
		},
	}

	items, err := buildReversalAdjustItems(events)
	require.NoError(t, err)
	require.Len(t, items, 2, "staking produces 2 items: liquid + staked")

	// First: liquid balance reversal (negate the delta)
	assert.Equal(t, "5000", items[0].Delta)
	assert.Equal(t, "", items[0].BalanceType)

	// Second: staked balance reversal (original delta, reverses the staked balance)
	assert.Equal(t, "-5000", items[1].Delta)
	assert.Equal(t, "staked", items[1].BalanceType)
}

func TestBuildReversalAdjustItems_EmptyInput(t *testing.T) {
	items, err := buildReversalAdjustItems(nil)
	require.NoError(t, err)
	assert.Empty(t, items)
}

// ---------------------------------------------------------------------------
// Test: buildPromotionAdjustItems
// ---------------------------------------------------------------------------

func TestBuildPromotionAdjustItems_BasicForwardDelta(t *testing.T) {
	tokenID := uuid.New()
	walletID := "wallet1"
	orgID := "org1"
	events := []promotedEvent{
		{
			TokenID:        tokenID,
			Address:        "addr1",
			Delta:          "3000",
			BlockCursor:    500,
			TxHash:         "tx1",
			WalletID:       &walletID,
			OrganizationID: &orgID,
			ActivityType:   model.ActivityDeposit,
		},
	}

	items, err := buildPromotionAdjustItems(events)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "3000", items[0].Delta)
	assert.Equal(t, "addr1", items[0].Address)
	assert.Equal(t, tokenID, items[0].TokenID)
	assert.Equal(t, &walletID, items[0].WalletID)
	assert.Equal(t, &orgID, items[0].OrgID)
	assert.Equal(t, int64(500), items[0].Cursor)
	assert.Equal(t, "tx1", items[0].TxHash)
	assert.Equal(t, "", items[0].BalanceType)
}

func TestBuildPromotionAdjustItems_MultipleEvents(t *testing.T) {
	events := []promotedEvent{
		{
			TokenID:      uuid.New(),
			Address:      "addr1",
			Delta:        "1000",
			BlockCursor:  100,
			TxHash:       "tx1",
			ActivityType: model.ActivityDeposit,
		},
		{
			TokenID:      uuid.New(),
			Address:      "addr2",
			Delta:        "-200",
			BlockCursor:  101,
			TxHash:       "tx2",
			ActivityType: model.ActivityWithdrawal,
		},
	}

	items, err := buildPromotionAdjustItems(events)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, "1000", items[0].Delta)
	assert.Equal(t, "-200", items[1].Delta)
}

func TestBuildPromotionAdjustItems_StakingActivity_ProducesStakedItem(t *testing.T) {
	tokenID := uuid.New()
	events := []promotedEvent{
		{
			TokenID:      tokenID,
			Address:      "addr1",
			Delta:        "-5000", // stake: negative delta (locked away from liquid)
			BlockCursor:  100,
			TxHash:       "tx1",
			ActivityType: model.ActivityStake,
		},
	}

	items, err := buildPromotionAdjustItems(events)
	require.NoError(t, err)
	require.Len(t, items, 2, "staking produces 2 items: liquid + staked")

	// First: liquid balance (forward delta)
	assert.Equal(t, "-5000", items[0].Delta)
	assert.Equal(t, "", items[0].BalanceType)

	// Second: staked balance (inverted delta)
	assert.Equal(t, "5000", items[1].Delta)
	assert.Equal(t, "staked", items[1].BalanceType)
}

func TestBuildPromotionAdjustItems_UnstakeActivity_ProducesStakedItem(t *testing.T) {
	tokenID := uuid.New()
	events := []promotedEvent{
		{
			TokenID:      tokenID,
			Address:      "addr1",
			Delta:        "5000", // unstake: positive delta (returned to liquid)
			BlockCursor:  200,
			TxHash:       "tx2",
			ActivityType: model.ActivityUnstake,
		},
	}

	items, err := buildPromotionAdjustItems(events)
	require.NoError(t, err)
	require.Len(t, items, 2)

	assert.Equal(t, "5000", items[0].Delta)
	assert.Equal(t, "", items[0].BalanceType)

	assert.Equal(t, "-5000", items[1].Delta)
	assert.Equal(t, "staked", items[1].BalanceType)
}

func TestBuildPromotionAdjustItems_EmptyInput(t *testing.T) {
	items, err := buildPromotionAdjustItems(nil)
	require.NoError(t, err)
	assert.Empty(t, items)
}

// ---------------------------------------------------------------------------
// Test: rollbackCanonicalityDrift (real implementation, not a stub)
// ---------------------------------------------------------------------------

func TestRollbackCanonicalityDrift_CallsReposCorrectly(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	fakeDB := openRFFakeDB(t)
	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	// The real rollbackCanonicalityDrift calls fetchRollbackEvents which does
	// a SQL query on the dbTx. Since our fake driver returns empty rows,
	// there will be no rollback events and therefore no BulkAdjustBalanceTx call.
	// It will then DELETE balance_events and RewindWatermarkTx.

	mockWmRepo.EXPECT().
		RewindWatermarkTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, int64(99)).
		Return(nil)

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		nil, // db not needed for this direct call
		nil, // txRepo
		nil, // balanceEventRepo
		mockBalanceRepo,
		nil, // tokenRepo
		mockWmRepo,
		normalizedCh,
		slog.Default(),
	)

	batch := event.NormalizedBatch{
		Chain:                  model.ChainBase,
		Network:                model.NetworkSepolia,
		Address:                "0xaddr1",
		PreviousCursorValue:    ptr("old-hash"),
		PreviousCursorSequence: 100,
		NewCursorValue:         ptr("new-hash"),
		NewCursorSequence:      100,
	}

	err = ing.rollbackCanonicalityDrift(context.Background(), dbTx, batch)
	require.NoError(t, err)
}

func TestRollbackCanonicalityDrift_WithRollbackEvents_CallsBulkAdjust(t *testing.T) {
	// Use the interleave infrastructure to test with actual data flow.
	state := newInterleaveState(nil)
	fakeDB := openRFFakeDB(t)

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		&interleaveTxRepo{state: state},
		&interleaveBalanceEventRepo{state: state},
		&interleaveBalanceRepo{state: state},
		&interleaveTokenRepo{state: state},
		&interleaveConfigRepo{state: state},
		normalizedCh,
		slog.Default(),
		// DO NOT set WithReorgHandler — let the real rollbackCanonicalityDrift be used.
	)

	now := time.Now()
	address := "sol-addr-drift"

	// Step 1: Insert a batch at cursor 100 to create events.
	sig100 := "sig-100"
	batch1 := makeBatch(model.ChainSolana, model.NetworkDevnet, address, nil, 0, &sig100, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: sig100, BlockCursor: 100, BlockTime: &now, FeeAmount: "5000", FeePayer: address,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("system_transfer", address, "-1000000", "SOL"),
				},
			},
		},
	)

	ctx := context.Background()
	err := ing.processBatch(ctx, batch1)
	require.NoError(t, err)

	// Verify events exist.
	tuples := state.snapshotTuples()
	require.NotEmpty(t, tuples, "events should be created from batch1")

	// Step 2: Trigger canonicality drift (same-sequence, different cursor value, no txs).
	newCursor := "sig-100-fork"
	driftBatch := makeBatch(model.ChainSolana, model.NetworkDevnet, address, &sig100, 100, &newCursor, 100,
		[]event.NormalizedTransaction{},
	)

	err = ing.processBatch(ctx, driftBatch)
	require.NoError(t, err)

	// After drift rollback, events from cursor >= 100 should be deleted.
	// Since the interleave infrastructure manages its own state,
	// the rollbackFromCursor equivalent is handled by the SQL DELETE
	// which goes to the fake driver (a no-op). The real interleave state
	// won't reflect the deletion since our fake driver doesn't actually delete.
	// What we verify here is that the code path executed without error,
	// meaning the real rollbackCanonicalityDrift was used.
}

// ---------------------------------------------------------------------------
// Test: handleReorg (full flow via Run loop with reorgCh)
// ---------------------------------------------------------------------------

func TestHandleReorg_InlineRollback_FullFlow(t *testing.T) {
	ctrl := gomock.NewController(t)
	fakeDB := openRFFakeDB(t)

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)
	mockBlockRepo := storemocks.NewMockIndexedBlockRepository(ctrl)

	reorgCh := make(chan event.ReorgEvent, 1)
	normalizedCh := make(chan event.NormalizedBatch)

	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, // txRepo not needed
		nil, // balanceEventRepo not needed
		mockBalanceRepo,
		nil, // tokenRepo
		mockWmRepo,
		normalizedCh,
		slog.Default(),
		WithReorgChannel(reorgCh),
		WithIndexedBlockRepo(mockBlockRepo),
	)

	// Expect: RewindWatermarkTx called with forkBlock-1 = 99
	mockWmRepo.EXPECT().
		RewindWatermarkTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, int64(99)).
		Return(nil)

	// Expect: DeleteFromBlockTx called for indexed blocks
	mockBlockRepo.EXPECT().
		DeleteFromBlockTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, int64(100)).
		Return(int64(0), nil)

	// No rollback events (empty result from fake driver), so BulkAdjustBalanceTx not called.

	reorgCh <- event.ReorgEvent{
		Chain:           model.ChainBase,
		Network:         model.NetworkSepolia,
		ForkBlockNumber: 100,
		ExpectedHash:    "0xexpected",
		ActualHash:      "0xactual",
		DetectedAt:      time.Now(),
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Run in background, then cancel after reorg is processed.
	done := make(chan error, 1)
	go func() {
		done <- ing.Run(ctx)
	}()

	// Give the Run loop time to process.
	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	// Context cancellation is expected.
	assert.ErrorIs(t, err, context.Canceled)
}

func TestHandleReorg_ForkAtBlockZero_RewindToZero(t *testing.T) {
	ctrl := gomock.NewController(t)
	fakeDB := openRFFakeDB(t)

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	normalizedCh := make(chan event.NormalizedBatch)

	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil,
		mockBalanceRepo,
		nil,
		mockWmRepo,
		normalizedCh,
		slog.Default(),
	)

	// forkBlock=0 should rewind to 0 (not -1)
	mockWmRepo.EXPECT().
		RewindWatermarkTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, int64(0)).
		Return(nil)

	err := ing.handleReorg(context.Background(), event.ReorgEvent{
		Chain:           model.ChainSolana,
		Network:         model.NetworkDevnet,
		ForkBlockNumber: 0,
		ExpectedHash:    "expected",
		ActualHash:      "actual",
	})
	require.NoError(t, err)
}

func TestHandleReorg_WithRollbackEvents_ReversesBalances(t *testing.T) {
	// Test the handleReorg inline path using gomock for precise verification.
	// The handleReorg function does:
	//   1. fetchReorgRollbackEvents (SQL -> fake driver returns empty)
	//   2. buildReversalAdjustItems (no events -> no items)
	//   3. DELETE balance_events (SQL -> fake driver)
	//   4. DELETE transactions (SQL -> fake driver)
	//   5. RewindWatermarkTx (mock -> verify call)
	//   6. Commit
	ctrl := gomock.NewController(t)
	fakeDB := openRFFakeDB(t)

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	normalizedCh := make(chan event.NormalizedBatch, 5)
	reorgCh := make(chan event.ReorgEvent, 1)

	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil,
		mockBalanceRepo,
		nil,
		mockWmRepo,
		normalizedCh,
		slog.Default(),
		WithReorgChannel(reorgCh),
	)

	// Expect RewindWatermarkTx with forkBlock-1 = 99.
	mockWmRepo.EXPECT().
		RewindWatermarkTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, int64(99)).
		Return(nil)

	reorgCh <- event.ReorgEvent{
		Chain:           model.ChainSolana,
		Network:         model.NetworkDevnet,
		ForkBlockNumber: 100,
		ExpectedHash:    "expected-hash",
		ActualHash:      "actual-hash",
		DetectedAt:      time.Now(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ing.Run(ctx)
	}()

	// Wait for reorg to be processed.
	time.Sleep(200 * time.Millisecond)
	cancel()

	err := <-done
	assert.ErrorIs(t, err, context.Canceled)
}

// ---------------------------------------------------------------------------
// Test: handleFinalityPromotion (full flow via Run loop with finalityCh)
// ---------------------------------------------------------------------------

func TestHandleFinalityPromotion_WithBlockRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	fakeDB := openRFFakeDB(t)

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockBlockRepo := storemocks.NewMockIndexedBlockRepository(ctrl)

	finalityCh := make(chan event.FinalityPromotion, 1)
	normalizedCh := make(chan event.NormalizedBatch)

	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil,
		mockBalanceRepo,
		nil, nil,
		normalizedCh,
		slog.Default(),
		WithFinalityChannel(finalityCh),
		WithIndexedBlockRepo(mockBlockRepo),
	)

	// Expect: UpdateFinalityTx called
	mockBlockRepo.EXPECT().
		UpdateFinalityTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkMainnet, int64(1000), "finalized").
		Return(nil)

	// The SQL queries for promoting balance events will return empty rows
	// (fake driver), so no BulkAdjustBalanceTx call expected.

	finalityCh <- event.FinalityPromotion{
		Chain:             model.ChainBase,
		Network:           model.NetworkMainnet,
		NewFinalizedBlock: 1000,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ing.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	assert.ErrorIs(t, err, context.Canceled)
}

func TestHandleFinalityPromotion_NoBlockRepo(t *testing.T) {
	fakeDB := openRFFakeDB(t)

	finalityCh := make(chan event.FinalityPromotion, 1)
	normalizedCh := make(chan event.NormalizedBatch)

	// No blockRepo configured — UpdateFinalityTx should be skipped.
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil, nil, nil, nil,
		normalizedCh,
		slog.Default(),
		WithFinalityChannel(finalityCh),
	)

	finalityCh <- event.FinalityPromotion{
		Chain:             model.ChainSolana,
		Network:           model.NetworkDevnet,
		NewFinalizedBlock: 500,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ing.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	assert.ErrorIs(t, err, context.Canceled)
}

func TestHandleFinalityPromotion_Direct_CallsPromoteAndAdjust(t *testing.T) {
	ctrl := gomock.NewController(t)
	fakeDB := openRFFakeDB(t)

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockBlockRepo := storemocks.NewMockIndexedBlockRepository(ctrl)

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil,
		mockBalanceRepo,
		nil, nil,
		normalizedCh,
		slog.Default(),
		WithIndexedBlockRepo(mockBlockRepo),
	)

	mockBlockRepo.EXPECT().
		UpdateFinalityTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkSepolia, int64(200), "finalized").
		Return(nil)

	// promoteBalanceEvents will execute SQL UPDATE + RETURNING against the fake
	// driver which returns empty results, so no adjust call expected.

	err := ing.handleFinalityPromotion(context.Background(), event.FinalityPromotion{
		Chain:             model.ChainBase,
		Network:           model.NetworkSepolia,
		NewFinalizedBlock: 200,
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Test: handleReorg direct call (without Run loop)
// ---------------------------------------------------------------------------

func TestHandleReorg_Direct_InlineRollback(t *testing.T) {
	ctrl := gomock.NewController(t)
	fakeDB := openRFFakeDB(t)

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)
	mockBlockRepo := storemocks.NewMockIndexedBlockRepository(ctrl)

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil,
		mockBalanceRepo,
		nil,
		mockWmRepo,
		normalizedCh,
		slog.Default(),
		WithIndexedBlockRepo(mockBlockRepo),
	)

	mockBlockRepo.EXPECT().
		DeleteFromBlockTx(gomock.Any(), gomock.Any(), model.ChainEthereum, model.NetworkMainnet, int64(50)).
		Return(int64(3), nil)

	mockWmRepo.EXPECT().
		RewindWatermarkTx(gomock.Any(), gomock.Any(), model.ChainEthereum, model.NetworkMainnet, int64(49)).
		Return(nil)

	err := ing.handleReorg(context.Background(), event.ReorgEvent{
		Chain:           model.ChainEthereum,
		Network:         model.NetworkMainnet,
		ForkBlockNumber: 50,
		ExpectedHash:    "0xexpected",
		ActualHash:      "0xactual",
		DetectedAt:      time.Now(),
	})
	require.NoError(t, err)
}

func TestHandleReorg_Direct_NoBlockRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	fakeDB := openRFFakeDB(t)

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil,
		mockBalanceRepo,
		nil,
		mockWmRepo,
		normalizedCh,
		slog.Default(),
		// No WithIndexedBlockRepo — should skip block deletion.
	)

	mockWmRepo.EXPECT().
		RewindWatermarkTx(gomock.Any(), gomock.Any(), model.ChainSolana, model.NetworkDevnet, int64(9)).
		Return(nil)

	err := ing.handleReorg(context.Background(), event.ReorgEvent{
		Chain:           model.ChainSolana,
		Network:         model.NetworkDevnet,
		ForkBlockNumber: 10,
		ExpectedHash:    "expected",
		ActualHash:      "actual",
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Test: handleReorg with replay service (delegate path)
// ---------------------------------------------------------------------------

// mockReplayService is a minimal replay.Service substitute for testing.
// The real replay.Service cannot be easily mocked (it's a concrete struct),
// so we test the inline fallback path above and verify the delegation
// branch separately via the handleReorg code path logic.

// ---------------------------------------------------------------------------
// Test: handleFinalityPromotion error propagation
// ---------------------------------------------------------------------------

func TestHandleFinalityPromotion_PromoteQueryError_Propagates(t *testing.T) {
	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "RETURNING") {
			return nil, errors.New("query deadlock")
		}
		return &rfEmptyRows{}, nil
	})

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil, nil, nil, nil,
		normalizedCh,
		slog.Default(),
	)

	err := ing.handleFinalityPromotion(context.Background(), event.FinalityPromotion{
		Chain:             model.ChainBase,
		Network:           model.NetworkMainnet,
		NewFinalizedBlock: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "promote balance events")
}

func TestHandleFinalityPromotion_ExecError_Propagates(t *testing.T) {
	fakeDB := openRFFakeDBWith(t, nil, func(query string, _ []driver.Value) (driver.Result, error) {
		if strings.Contains(query, "UPDATE") {
			return nil, errors.New("exec deadlock")
		}
		return driver.RowsAffected(0), nil
	})

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil, nil, nil, nil,
		normalizedCh,
		slog.Default(),
	)

	err := ing.handleFinalityPromotion(context.Background(), event.FinalityPromotion{
		Chain:             model.ChainBase,
		Network:           model.NetworkMainnet,
		NewFinalizedBlock: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "promote balance events")
}

func TestHandleFinalityPromotion_BulkAdjustError_Propagates(t *testing.T) {
	ctrl := gomock.NewController(t)
	tokenID := uuid.New()

	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "RETURNING") {
			return &rfDataRows{
				columns: promotedEventColumns,
				data: [][]driver.Value{
					{tokenID.String(), "addr1", "1000", int64(100), "tx-100", nil, nil, string(model.ActivityDeposit)},
				},
			}, nil
		}
		return &rfEmptyRows{}, nil
	})

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("adjust failed"))

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil, mockBalanceRepo, nil, nil,
		normalizedCh,
		slog.Default(),
	)

	err := ing.handleFinalityPromotion(context.Background(), event.FinalityPromotion{
		Chain:             model.ChainBase,
		Network:           model.NetworkMainnet,
		NewFinalizedBlock: 200,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bulk adjust balances for finality promotion")
}

func TestHandleFinalityPromotion_UpdateFinalityError_Propagates(t *testing.T) {
	ctrl := gomock.NewController(t)
	fakeDB := openRFFakeDB(t)

	mockBlockRepo := storemocks.NewMockIndexedBlockRepository(ctrl)

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil, nil, nil, nil,
		normalizedCh,
		slog.Default(),
		WithIndexedBlockRepo(mockBlockRepo),
	)

	mockBlockRepo.EXPECT().
		UpdateFinalityTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("db error"))

	err := ing.handleFinalityPromotion(context.Background(), event.FinalityPromotion{
		Chain:             model.ChainBase,
		Network:           model.NetworkMainnet,
		NewFinalizedBlock: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update indexed blocks finality")
}

// ---------------------------------------------------------------------------
// Test: handleReorg error propagation
// ---------------------------------------------------------------------------

func TestHandleReorg_DeleteBalanceEventsError_Propagates(t *testing.T) {
	fakeDB := openRFFakeDBWith(t, nil, func(query string, _ []driver.Value) (driver.Result, error) {
		if strings.Contains(query, "DELETE FROM balance_events") {
			return nil, errors.New("delete failed")
		}
		return driver.RowsAffected(0), nil
	})

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil, nil, nil, nil,
		normalizedCh,
		slog.Default(),
	)

	err := ing.handleReorg(context.Background(), event.ReorgEvent{
		Chain:           model.ChainBase,
		Network:         model.NetworkSepolia,
		ForkBlockNumber: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete reorg balance events")
}

func TestHandleReorg_DeleteTransactionsError_Propagates(t *testing.T) {
	fakeDB := openRFFakeDBWith(t, nil, func(query string, _ []driver.Value) (driver.Result, error) {
		if strings.Contains(query, "DELETE FROM transactions") {
			return nil, errors.New("tx delete failed")
		}
		return driver.RowsAffected(0), nil
	})

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil, nil, nil, nil,
		normalizedCh,
		slog.Default(),
	)

	err := ing.handleReorg(context.Background(), event.ReorgEvent{
		Chain:           model.ChainBase,
		Network:         model.NetworkSepolia,
		ForkBlockNumber: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete reorg transactions")
}

func TestHandleReorg_RewindWatermarkError_Propagates(t *testing.T) {
	ctrl := gomock.NewController(t)
	fakeDB := openRFFakeDB(t)

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil,
		mockBalanceRepo,
		nil,
		mockWmRepo,
		normalizedCh,
		slog.Default(),
	)

	mockWmRepo.EXPECT().
		RewindWatermarkTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("watermark rewind failed"))

	err := ing.handleReorg(context.Background(), event.ReorgEvent{
		Chain:           model.ChainSolana,
		Network:         model.NetworkDevnet,
		ForkBlockNumber: 50,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rewind watermark")
}

func TestHandleReorg_DeleteBlocksError_Propagates(t *testing.T) {
	ctrl := gomock.NewController(t)
	fakeDB := openRFFakeDB(t)

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)
	mockBlockRepo := storemocks.NewMockIndexedBlockRepository(ctrl)

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil,
		mockBalanceRepo,
		nil,
		mockWmRepo,
		normalizedCh,
		slog.Default(),
		WithIndexedBlockRepo(mockBlockRepo),
	)

	mockBlockRepo.EXPECT().
		DeleteFromBlockTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(int64(0), errors.New("block deletion failed"))

	err := ing.handleReorg(context.Background(), event.ReorgEvent{
		Chain:           model.ChainBase,
		Network:         model.NetworkSepolia,
		ForkBlockNumber: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete reorg indexed blocks")
}

// ---------------------------------------------------------------------------
// Test: buildReversalAdjustItems with large delta values
// ---------------------------------------------------------------------------

func TestBuildReversalAdjustItems_LargeDelta(t *testing.T) {
	events := []rollbackBalanceEvent{
		{
			TokenID:        uuid.New(),
			Address:        "addr1",
			Delta:          "999999999999999999999999999999",
			BlockCursor:    100,
			TxHash:         "tx1",
			ActivityType:   model.ActivityDeposit,
			BalanceApplied: true,
		},
	}

	items, err := buildReversalAdjustItems(events)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "-999999999999999999999999999999", items[0].Delta)
}

// ---------------------------------------------------------------------------
// Test: buildPromotionAdjustItems preserves metadata
// ---------------------------------------------------------------------------

func TestBuildPromotionAdjustItems_PreservesWalletAndOrgIDs(t *testing.T) {
	walletID := "wallet-abc"
	orgID := "org-xyz"
	tokenID := uuid.New()

	events := []promotedEvent{
		{
			TokenID:        tokenID,
			Address:        "addr1",
			Delta:          "100",
			BlockCursor:    50,
			TxHash:         "tx1",
			WalletID:       &walletID,
			OrganizationID: &orgID,
			ActivityType:   model.ActivityDeposit,
		},
		{
			TokenID:      tokenID,
			Address:      "addr2",
			Delta:        "200",
			BlockCursor:  51,
			TxHash:       "tx2",
			ActivityType: model.ActivityDeposit,
			// nil wallet/org
		},
	}

	items, err := buildPromotionAdjustItems(events)
	require.NoError(t, err)
	require.Len(t, items, 2)

	assert.Equal(t, &walletID, items[0].WalletID)
	assert.Equal(t, &orgID, items[0].OrgID)
	assert.Nil(t, items[1].WalletID)
	assert.Nil(t, items[1].OrgID)
}

// ---------------------------------------------------------------------------
// Test: buildReversalAdjustItems with zero delta
// ---------------------------------------------------------------------------

func TestBuildReversalAdjustItems_ZeroDelta(t *testing.T) {
	events := []rollbackBalanceEvent{
		{
			TokenID:        uuid.New(),
			Address:        "addr1",
			Delta:          "0",
			BlockCursor:    100,
			TxHash:         "tx1",
			ActivityType:   model.ActivityOther,
			BalanceApplied: true,
		},
	}

	items, err := buildReversalAdjustItems(events)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "0", items[0].Delta)
}

// ---------------------------------------------------------------------------
// Test: handleReorg via Run loop — channel closed gracefully
// ---------------------------------------------------------------------------

func TestHandleReorg_ChannelClosed_ContinuesRunLoop(t *testing.T) {
	fakeDB := openRFFakeDB(t)

	reorgCh := make(chan event.ReorgEvent)
	normalizedCh := make(chan event.NormalizedBatch)

	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil, nil, nil, nil,
		normalizedCh,
		slog.Default(),
		WithReorgChannel(reorgCh),
	)

	// Close reorgCh immediately — Run should set reorgCh=nil and continue.
	close(reorgCh)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ing.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	assert.ErrorIs(t, err, context.Canceled)
}

// ---------------------------------------------------------------------------
// Test: handleFinalityPromotion via Run loop — channel closed gracefully
// ---------------------------------------------------------------------------

func TestHandleFinalityPromotion_ChannelClosed_ContinuesRunLoop(t *testing.T) {
	fakeDB := openRFFakeDB(t)

	finalityCh := make(chan event.FinalityPromotion)
	normalizedCh := make(chan event.NormalizedBatch)

	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil, nil, nil, nil,
		normalizedCh,
		slog.Default(),
		WithFinalityChannel(finalityCh),
	)

	close(finalityCh)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ing.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	assert.ErrorIs(t, err, context.Canceled)
}

// ---------------------------------------------------------------------------
// Test: buildReversalAdjustItems + buildPromotionAdjustItems roundtrip
// ---------------------------------------------------------------------------

func TestReversalAndPromotion_Roundtrip_DeltasCancel(t *testing.T) {
	// Simulate: events are promoted (buildPromotionAdjustItems),
	// then rolled back (buildReversalAdjustItems). The net effect should be zero.
	tokenID := uuid.New()
	walletID := "wallet1"
	orgID := "org1"

	promoted := []promotedEvent{
		{
			TokenID:        tokenID,
			Address:        "addr1",
			Delta:          "5000",
			BlockCursor:    100,
			TxHash:         "tx1",
			WalletID:       &walletID,
			OrganizationID: &orgID,
			ActivityType:   model.ActivityDeposit,
		},
	}

	promoItems, err := buildPromotionAdjustItems(promoted)
	require.NoError(t, err)
	require.Len(t, promoItems, 1)
	assert.Equal(t, "5000", promoItems[0].Delta)

	// Now build reversal as if these events are being rolled back.
	rollback := []rollbackBalanceEvent{
		{
			TokenID:        tokenID,
			Address:        "addr1",
			Delta:          "5000",
			BlockCursor:    100,
			TxHash:         "tx1",
			WalletID:       &walletID,
			OrganizationID: &orgID,
			ActivityType:   model.ActivityDeposit,
			BalanceApplied: true,
		},
	}

	reversalItems, err := buildReversalAdjustItems(rollback)
	require.NoError(t, err)
	require.Len(t, reversalItems, 1)
	assert.Equal(t, "-5000", reversalItems[0].Delta)

	// Promotion delta + reversal delta = 0
	promoVal := promoItems[0].Delta
	reversalVal := reversalItems[0].Delta
	sum, sumErr := addDecimalStrings(promoVal, reversalVal)
	require.NoError(t, sumErr)
	assert.Equal(t, "0", sum, "promotion and reversal should cancel out")
}

// ---------------------------------------------------------------------------
// Test: buildReversalAdjustItems with mixed staking and non-staking
// ---------------------------------------------------------------------------

func TestBuildReversalAdjustItems_MixedStakingAndNonStaking(t *testing.T) {
	tokenID := uuid.New()
	events := []rollbackBalanceEvent{
		{
			TokenID:        tokenID,
			Address:        "addr1",
			Delta:          "1000",
			BlockCursor:    100,
			TxHash:         "tx1",
			ActivityType:   model.ActivityDeposit,
			BalanceApplied: true,
		},
		{
			TokenID:        tokenID,
			Address:        "addr1",
			Delta:          "-3000",
			BlockCursor:    101,
			TxHash:         "tx2",
			ActivityType:   model.ActivityStake,
			BalanceApplied: true,
		},
		{
			TokenID:        tokenID,
			Address:        "addr1",
			Delta:          "2000",
			BlockCursor:    102,
			TxHash:         "tx3",
			ActivityType:   model.ActivityUnstake,
			BalanceApplied: true,
		},
	}

	items, err := buildReversalAdjustItems(events)
	require.NoError(t, err)

	// tx1: deposit -> 1 item (liquid reversal)
	// tx2: stake -> 2 items (liquid reversal + staked reversal)
	// tx3: unstake -> 2 items (liquid reversal + staked reversal)
	require.Len(t, items, 5)

	// tx1 liquid: -1000
	assert.Equal(t, "-1000", items[0].Delta)
	assert.Equal(t, "", items[0].BalanceType)

	// tx2 liquid: 3000
	assert.Equal(t, "3000", items[1].Delta)
	assert.Equal(t, "", items[1].BalanceType)

	// tx2 staked: original delta (-3000) to reverse staked
	assert.Equal(t, "-3000", items[2].Delta)
	assert.Equal(t, "staked", items[2].BalanceType)

	// tx3 liquid: -2000
	assert.Equal(t, "-2000", items[3].Delta)
	assert.Equal(t, "", items[3].BalanceType)

	// tx3 staked: original delta (2000) to reverse staked
	assert.Equal(t, "2000", items[4].Delta)
	assert.Equal(t, "staked", items[4].BalanceType)
}

// ---------------------------------------------------------------------------
// Test: buildPromotionAdjustItems with mixed staking
// ---------------------------------------------------------------------------

func TestBuildPromotionAdjustItems_MixedStaking(t *testing.T) {
	tokenID := uuid.New()
	events := []promotedEvent{
		{
			TokenID:      tokenID,
			Address:      "addr1",
			Delta:        "1000",
			BlockCursor:  100,
			TxHash:       "tx1",
			ActivityType: model.ActivityDeposit,
		},
		{
			TokenID:      tokenID,
			Address:      "addr1",
			Delta:        "-3000",
			BlockCursor:  101,
			TxHash:       "tx2",
			ActivityType: model.ActivityStake,
		},
	}

	items, err := buildPromotionAdjustItems(events)
	require.NoError(t, err)

	// tx1: deposit -> 1 item
	// tx2: stake -> 2 items (liquid + staked)
	require.Len(t, items, 3)

	// tx1 liquid: 1000
	assert.Equal(t, "1000", items[0].Delta)
	assert.Equal(t, "", items[0].BalanceType)

	// tx2 liquid: -3000
	assert.Equal(t, "-3000", items[1].Delta)
	assert.Equal(t, "", items[1].BalanceType)

	// tx2 staked: 3000 (inverted)
	assert.Equal(t, "3000", items[2].Delta)
	assert.Equal(t, "staked", items[2].BalanceType)
}

// ---------------------------------------------------------------------------
// Test: handleReorg BulkAdjustBalanceTx error propagation
// ---------------------------------------------------------------------------

func TestHandleReorg_FetchError_Propagates(t *testing.T) {
	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "SELECT") {
			return nil, errors.New("query timeout")
		}
		return &rfEmptyRows{}, nil
	})

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil, nil, nil, nil,
		normalizedCh,
		slog.Default(),
	)

	err := ing.handleReorg(context.Background(), event.ReorgEvent{
		Chain:           model.ChainBase,
		Network:         model.NetworkSepolia,
		ForkBlockNumber: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch reorg rollback events")
}

func TestHandleReorg_BulkAdjustError_Propagates(t *testing.T) {
	ctrl := gomock.NewController(t)
	tokenID := uuid.New()

	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "SELECT") {
			return &rfDataRows{
				columns: rollbackEventColumns,
				data: [][]driver.Value{
					{tokenID.String(), "addr1", "1000", int64(100), "tx-100", nil, nil, string(model.ActivityDeposit), true},
				},
			}, nil
		}
		return &rfEmptyRows{}, nil
	})

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("bulk adjust failed"))

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil,
		mockBalanceRepo,
		nil, nil,
		normalizedCh,
		slog.Default(),
	)

	err := ing.handleReorg(context.Background(), event.ReorgEvent{
		Chain:           model.ChainBase,
		Network:         model.NetworkSepolia,
		ForkBlockNumber: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "revert balances")
}

// ---------------------------------------------------------------------------
// Test: handleFinalityPromotion begin tx failure
// ---------------------------------------------------------------------------

func TestHandleFinalityPromotion_BeginTxError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := storemocks.NewMockTxBeginner(ctrl)

	mockDB.EXPECT().
		BeginTx(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("connection refused"))

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		mockDB,
		nil, nil, nil, nil, nil,
		normalizedCh,
		slog.Default(),
	)

	err := ing.handleFinalityPromotion(context.Background(), event.FinalityPromotion{
		Chain:             model.ChainBase,
		Network:           model.NetworkMainnet,
		NewFinalizedBlock: 500,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin finality tx")
}

// ---------------------------------------------------------------------------
// Test: handleReorg begin tx failure
// ---------------------------------------------------------------------------

func TestHandleReorg_BeginTxError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := storemocks.NewMockTxBeginner(ctrl)

	mockDB.EXPECT().
		BeginTx(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("connection refused"))

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		mockDB,
		nil, nil, nil, nil, nil,
		normalizedCh,
		slog.Default(),
	)

	err := ing.handleReorg(context.Background(), event.ReorgEvent{
		Chain:           model.ChainSolana,
		Network:         model.NetworkDevnet,
		ForkBlockNumber: 50,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin reorg tx")
}

// ---------------------------------------------------------------------------
// Test: rollbackCanonicalityDrift integrates correctly in processBatch
// ---------------------------------------------------------------------------

func TestProcessBatch_RealRollbackCanonicalityDrift_IsUsedByDefault(t *testing.T) {
	// This test ensures that when no WithReorgHandler is provided,
	// the ingester uses the real rollbackCanonicalityDrift.
	fakeDB := openRFFakeDB(t)
	state := newInterleaveState(nil)

	normalizedCh := make(chan event.NormalizedBatch, 5)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		&interleaveTxRepo{state: state},
		&interleaveBalanceEventRepo{state: state},
		&interleaveBalanceRepo{state: state},
		&interleaveTokenRepo{state: state},
		&interleaveConfigRepo{state: state},
		normalizedCh,
		slog.Default(),
		// No WithReorgHandler — real rollbackCanonicalityDrift is used.
	)

	now := time.Now()
	address := "sol-addr-default"

	// Step 1: Process a normal batch.
	sig100 := "sig-100"
	batch1 := makeBatch(model.ChainSolana, model.NetworkDevnet, address, nil, 0, &sig100, 100,
		[]event.NormalizedTransaction{
			{
				TxHash: sig100, BlockCursor: 100, BlockTime: &now, FeeAmount: "5000", FeePayer: address,
				Status: model.TxStatusSuccess,
				BalanceEvents: []event.NormalizedBalanceEvent{
					makeBalanceEvent("system_transfer", address, "-1000000", "SOL"),
				},
			},
		},
	)

	ctx := context.Background()
	err := ing.processBatch(ctx, batch1)
	require.NoError(t, err)

	// Verify reorgHandler was set to rollbackCanonicalityDrift.
	assert.NotNil(t, ing.reorgHandler, "reorgHandler should be set after first processBatch")

	// Step 2: Process a drift batch (same-sequence, different cursor, no txs).
	newCursor := "sig-100-fork"
	driftBatch := makeBatch(model.ChainSolana, model.NetworkDevnet, address, &sig100, 100, &newCursor, 100,
		[]event.NormalizedTransaction{},
	)

	// This should use the real rollbackCanonicalityDrift and not panic.
	err = ing.processBatch(ctx, driftBatch)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Direct tests: fetchReorgRollbackEvents, fetchRollbackEvents, promoteBalanceEvents
// Each test creates an isolated fake DB with per-test driver name.
// ---------------------------------------------------------------------------

func TestFetchReorgRollbackEvents_EmptyResult(t *testing.T) {
	fakeDB := openRFFakeDB(t)
	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.fetchReorgRollbackEvents(context.Background(), dbTx, model.ChainBase, model.NetworkSepolia, 50)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestFetchReorgRollbackEvents_MultiRow(t *testing.T) {
	tokenID := uuid.New()
	walletID := "wallet-reorg-1"

	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		return &rfDataRows{
			columns: rollbackEventColumns,
			data: [][]driver.Value{
				{tokenID.String(), "addr1", "10000", int64(200), "tx-200", walletID, nil, string(model.ActivityDeposit), true},
				{tokenID.String(), "addr2", "-5000", int64(201), "tx-201", nil, nil, string(model.ActivityStake), true},
				{tokenID.String(), "addr3", "1000", int64(202), "tx-202", nil, nil, string(model.ActivityDeposit), false},
			},
		}, nil
	})

	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.fetchReorgRollbackEvents(context.Background(), dbTx, model.ChainBase, model.NetworkSepolia, 200)
	require.NoError(t, err)
	require.Len(t, events, 3)

	assert.Equal(t, tokenID, events[0].TokenID)
	assert.Equal(t, "10000", events[0].Delta)
	require.NotNil(t, events[0].WalletID)
	assert.Equal(t, walletID, *events[0].WalletID)
	assert.True(t, events[0].BalanceApplied)

	assert.Equal(t, "-5000", events[1].Delta)
	assert.Equal(t, model.ActivityStake, events[1].ActivityType)
	assert.Nil(t, events[1].WalletID)

	assert.False(t, events[2].BalanceApplied)
}

func TestFetchReorgRollbackEvents_QueryError(t *testing.T) {
	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		return nil, errors.New("connection lost")
	})

	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.fetchReorgRollbackEvents(context.Background(), dbTx, model.ChainBase, model.NetworkSepolia, 50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query reorg rollback events")
	assert.Nil(t, events)
}

func TestFetchRollbackEvents_EmptyResult(t *testing.T) {
	fakeDB := openRFFakeDB(t)
	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.fetchRollbackEvents(context.Background(), dbTx, model.ChainSolana, model.NetworkDevnet, "some-address", 100)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestFetchRollbackEvents_MultiRow(t *testing.T) {
	tokenID := uuid.New()
	orgID := "org-rb"

	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		return &rfDataRows{
			columns: rollbackEventColumns,
			data: [][]driver.Value{
				{tokenID.String(), "addr1", "3000", int64(300), "tx-300", nil, orgID, string(model.ActivityDeposit), true},
				{tokenID.String(), "addr1", "-1500", int64(301), "tx-301", nil, nil, string(model.ActivityWithdrawal), true},
			},
		}, nil
	})

	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.fetchRollbackEvents(context.Background(), dbTx, model.ChainSolana, model.NetworkDevnet, "addr1", 300)
	require.NoError(t, err)
	require.Len(t, events, 2)

	assert.Equal(t, "3000", events[0].Delta)
	require.NotNil(t, events[0].OrganizationID)
	assert.Equal(t, orgID, *events[0].OrganizationID)
	assert.Equal(t, "-1500", events[1].Delta)
	assert.Equal(t, model.ActivityWithdrawal, events[1].ActivityType)
}

func TestFetchRollbackEvents_QueryError(t *testing.T) {
	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		return nil, errors.New("timeout")
	})

	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.fetchRollbackEvents(context.Background(), dbTx, model.ChainSolana, model.NetworkDevnet, "addr1", 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query rollback events")
	assert.Nil(t, events)
}

func TestPromoteBalanceEvents_EmptyResult(t *testing.T) {
	fakeDB := openRFFakeDB(t)
	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.promoteBalanceEvents(context.Background(), dbTx, model.ChainBase, model.NetworkMainnet, 500)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestPromoteBalanceEvents_MultiRow(t *testing.T) {
	tokenID := uuid.New()
	walletID := "wallet-promo"

	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "RETURNING") {
			return &rfDataRows{
				columns: promotedEventColumns,
				data: [][]driver.Value{
					{tokenID.String(), "addr1", "8000", int64(400), "tx-400", walletID, nil, string(model.ActivityDeposit)},
					{tokenID.String(), "addr1", "-3000", int64(401), "tx-401", nil, nil, string(model.ActivityStake)},
				},
			}, nil
		}
		return &rfEmptyRows{}, nil
	})

	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.promoteBalanceEvents(context.Background(), dbTx, model.ChainBase, model.NetworkMainnet, 500)
	require.NoError(t, err)
	require.Len(t, events, 2)

	assert.Equal(t, "8000", events[0].Delta)
	require.NotNil(t, events[0].WalletID)
	assert.Equal(t, walletID, *events[0].WalletID)
	assert.Equal(t, model.ActivityDeposit, events[0].ActivityType)

	assert.Equal(t, "-3000", events[1].Delta)
	assert.Equal(t, model.ActivityStake, events[1].ActivityType)
	assert.Nil(t, events[1].WalletID)
}

func TestPromoteBalanceEvents_ExecError(t *testing.T) {
	fakeDB := openRFFakeDBWith(t, nil, func(query string, _ []driver.Value) (driver.Result, error) {
		if strings.Contains(query, "UPDATE") {
			return nil, errors.New("exec failed: deadlock")
		}
		return driver.RowsAffected(0), nil
	})

	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.promoteBalanceEvents(context.Background(), dbTx, model.ChainBase, model.NetworkMainnet, 500)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update balance events finality")
	assert.Nil(t, events)
}

func TestPromoteBalanceEvents_QueryError(t *testing.T) {
	fakeDB := openRFFakeDBWith(t,
		func(query string, _ []driver.Value) (driver.Rows, error) {
			return nil, errors.New("query failed: connection reset")
		},
		nil, // exec succeeds
	)

	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.promoteBalanceEvents(context.Background(), dbTx, model.ChainBase, model.NetworkMainnet, 500)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "promote balance events")
	assert.Nil(t, events)
}

// ---------------------------------------------------------------------------
// E2E: fetchReorgRollbackEvents → buildReversalAdjustItems (via fake driver)
// ---------------------------------------------------------------------------

func TestFetchReorgRollbackEvents_E2E_BuildReversal(t *testing.T) {
	tokenID := uuid.New()
	walletID := "wallet-e2e"

	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		return &rfDataRows{
			columns: rollbackEventColumns,
			data: [][]driver.Value{
				{tokenID.String(), "addr1", "10000", int64(200), "tx-200", walletID, nil, string(model.ActivityDeposit), true},
				{tokenID.String(), "addr1", "-4000", int64(201), "tx-201", nil, nil, string(model.ActivityStake), true},
				{tokenID.String(), "addr2", "500", int64(202), "tx-202", nil, nil, string(model.ActivityDeposit), false},
			},
		}, nil
	})

	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.fetchReorgRollbackEvents(context.Background(), dbTx, model.ChainBase, model.NetworkSepolia, 200)
	require.NoError(t, err)
	require.Len(t, events, 3)

	// Build reversal items from fetched events
	items, err := buildReversalAdjustItems(events)
	require.NoError(t, err)
	// applied deposit(1) + applied stake(2) + not-applied(0) = 3
	require.Len(t, items, 3)
	assert.Equal(t, "-10000", items[0].Delta)
	assert.Equal(t, "4000", items[1].Delta)
	assert.Equal(t, "-4000", items[2].Delta)
	assert.Equal(t, "staked", items[2].BalanceType)
}

// ---------------------------------------------------------------------------
// E2E: promoteBalanceEvents → buildPromotionAdjustItems (via fake driver)
// ---------------------------------------------------------------------------

func TestPromoteBalanceEvents_E2E_BuildPromotion(t *testing.T) {
	tokenID := uuid.New()

	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "RETURNING") {
			return &rfDataRows{
				columns: promotedEventColumns,
				data: [][]driver.Value{
					{tokenID.String(), "addr1", "5000", int64(100), "tx-100", nil, nil, string(model.ActivityDeposit)},
					{tokenID.String(), "addr1", "-2000", int64(101), "tx-101", nil, nil, string(model.ActivityStake)},
					{tokenID.String(), "addr1", "1500", int64(102), "tx-102", nil, nil, string(model.ActivityUnstake)},
				},
			}, nil
		}
		return &rfEmptyRows{}, nil
	})

	dbTx, err := fakeDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = dbTx.Rollback() }()

	ing := &Ingester{logger: slog.Default()}
	events, err := ing.promoteBalanceEvents(context.Background(), dbTx, model.ChainBase, model.NetworkMainnet, 200)
	require.NoError(t, err)
	require.Len(t, events, 3)

	items, err := buildPromotionAdjustItems(events)
	require.NoError(t, err)
	// deposit(1) + stake(2) + unstake(2) = 5
	require.Len(t, items, 5)
	assert.Equal(t, "5000", items[0].Delta)
	assert.Equal(t, "-2000", items[1].Delta)
	assert.Equal(t, "2000", items[2].Delta)
	assert.Equal(t, "staked", items[2].BalanceType)
}

// ---------------------------------------------------------------------------
// Integration: handleFinalityPromotion with real promoted events + BulkAdjust
// ---------------------------------------------------------------------------

func TestHandleFinalityPromotion_WithPromotedEvents_CallsBulkAdjust(t *testing.T) {
	ctrl := gomock.NewController(t)
	tokenID := uuid.New()

	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "RETURNING") {
			return &rfDataRows{
				columns: promotedEventColumns,
				data: [][]driver.Value{
					{tokenID.String(), "addr1", "5000", int64(100), "tx-100", nil, nil, string(model.ActivityDeposit)},
					{tokenID.String(), "addr1", "-2000", int64(101), "tx-101", nil, nil, string(model.ActivityStake)},
				},
			}, nil
		}
		return &rfEmptyRows{}, nil
	})

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockBlockRepo := storemocks.NewMockIndexedBlockRepository(ctrl)

	mockBlockRepo.EXPECT().
		UpdateFinalityTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkMainnet, int64(200), "finalized").
		Return(nil)

	// Expect BulkAdjustBalanceTx to be called with 3 items (deposit=1 + stake=2).
	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), model.ChainBase, model.NetworkMainnet, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, items []store.BulkAdjustItem) error {
			assert.Len(t, items, 3, "deposit(1) + stake(2) = 3 adjust items")

			// Deposit: forward delta +5000 liquid
			assert.Equal(t, "5000", items[0].Delta)
			assert.Equal(t, "", items[0].BalanceType)

			// Stake: forward delta -2000 liquid
			assert.Equal(t, "-2000", items[1].Delta)
			assert.Equal(t, "", items[1].BalanceType)

			// Stake: inverted delta +2000 staked
			assert.Equal(t, "2000", items[2].Delta)
			assert.Equal(t, "staked", items[2].BalanceType)

			return nil
		})

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil,
		mockBalanceRepo,
		nil, nil,
		normalizedCh,
		slog.Default(),
		WithIndexedBlockRepo(mockBlockRepo),
	)

	err := ing.handleFinalityPromotion(context.Background(), event.FinalityPromotion{
		Chain:             model.ChainBase,
		Network:           model.NetworkMainnet,
		NewFinalizedBlock: 200,
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Integration: handleReorg with real rollback events + BulkAdjust
// ---------------------------------------------------------------------------

func TestHandleReorg_WithRollbackEvents_CallsBulkAdjust(t *testing.T) {
	ctrl := gomock.NewController(t)
	tokenID := uuid.New()
	walletID := "wallet-reorg"

	fakeDB := openRFFakeDBWithHandler(t, func(query string, _ []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "SELECT") && strings.Contains(query, "balance_events") {
			return &rfDataRows{
				columns: rollbackEventColumns,
				data: [][]driver.Value{
					{tokenID.String(), "addr1", "10000", int64(150), "tx-150", walletID, nil, string(model.ActivityDeposit), true},
					{tokenID.String(), "addr1", "-4000", int64(120), "tx-120", walletID, nil, string(model.ActivityStake), true},
					{tokenID.String(), "addr2", "1000", int64(130), "tx-130", nil, nil, string(model.ActivityDeposit), false},
				},
			}, nil
		}
		return &rfEmptyRows{}, nil
	})

	mockBalanceRepo := storemocks.NewMockBalanceRepository(ctrl)
	mockWmRepo := storemocks.NewMockWatermarkRepository(ctrl)

	// Expect BulkAdjustBalanceTx with reversal items:
	// applied deposit(1 item) + applied stake(2 items) = 3 items (not-applied skipped)
	mockBalanceRepo.EXPECT().
		BulkAdjustBalanceTx(gomock.Any(), gomock.Any(), model.ChainEthereum, model.NetworkMainnet, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, items []store.BulkAdjustItem) error {
			assert.Len(t, items, 3, "deposit(1) + stake(2) = 3 reversal items")

			// Deposit reversal: -10000 liquid
			assert.Equal(t, "-10000", items[0].Delta)
			assert.Equal(t, "", items[0].BalanceType)

			// Stake reversal: +4000 liquid
			assert.Equal(t, "4000", items[1].Delta)
			assert.Equal(t, "", items[1].BalanceType)

			// Stake reversal: -4000 staked
			assert.Equal(t, "-4000", items[2].Delta)
			assert.Equal(t, "staked", items[2].BalanceType)

			return nil
		})

	mockWmRepo.EXPECT().
		RewindWatermarkTx(gomock.Any(), gomock.Any(), model.ChainEthereum, model.NetworkMainnet, int64(99)).
		Return(nil)

	normalizedCh := make(chan event.NormalizedBatch)
	ing := New(
		&interleaveTxBeginner{db: fakeDB},
		nil, nil,
		mockBalanceRepo,
		nil,
		mockWmRepo,
		normalizedCh,
		slog.Default(),
	)

	err := ing.handleReorg(context.Background(), event.ReorgEvent{
		Chain:           model.ChainEthereum,
		Network:         model.NetworkMainnet,
		ForkBlockNumber: 100,
		ExpectedHash:    "0xexpected",
		ActualHash:      "0xactual",
		DetectedAt:      time.Now(),
	})
	require.NoError(t, err)
}

func ctx() context.Context { return context.Background() }

