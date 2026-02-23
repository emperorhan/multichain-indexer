package ingester

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// mockRowScanner: in-memory rowScanner for testing scan functions directly,
// without any database/sql driver dependency.
// ---------------------------------------------------------------------------

type mockRow struct {
	values []any
}

type mockRowScanner struct {
	rows    []mockRow
	idx     int
	scanErr error // injected error on Scan()
	iterErr error // returned by Err() after iteration
}

func (m *mockRowScanner) Next() bool {
	return m.idx < len(m.rows)
}

func (m *mockRowScanner) Scan(dest ...any) error {
	if m.scanErr != nil {
		return m.scanErr
	}
	row := m.rows[m.idx]
	m.idx++
	for i, val := range row.values {
		if i >= len(dest) {
			break
		}
		switch d := dest[i].(type) {
		case *uuid.UUID:
			*d = val.(uuid.UUID)
		case *string:
			*d = val.(string)
		case *int64:
			*d = val.(int64)
		case *bool:
			*d = val.(bool)
		case *model.ActivityType:
			*d = val.(model.ActivityType)
		case *sql.NullString:
			if val == nil {
				d.Valid = false
				d.String = ""
			} else {
				d.Valid = true
				d.String = val.(string)
			}
		}
	}
	return nil
}

func (m *mockRowScanner) Err() error {
	return m.iterErr
}

// makeRollbackRow creates a mockRow for scanRollbackEvents (9 columns).
func makeRollbackRow(
	tokenID uuid.UUID, address, delta string, blockCursor int64, txHash string,
	walletID, orgID any, activityType model.ActivityType, balanceApplied bool,
) mockRow {
	return mockRow{values: []any{tokenID, address, delta, blockCursor, txHash, walletID, orgID, activityType, balanceApplied}}
}

// makePromotedRow creates a mockRow for scanPromotedEvents (8 columns).
func makePromotedRow(
	tokenID uuid.UUID, address, delta string, blockCursor int64, txHash string,
	walletID, orgID any, activityType model.ActivityType,
) mockRow {
	return mockRow{values: []any{tokenID, address, delta, blockCursor, txHash, walletID, orgID, activityType}}
}

// ---------------------------------------------------------------------------
// Tests: scanRollbackEvents
// ---------------------------------------------------------------------------

func TestScanRollbackEvents_Empty(t *testing.T) {
	scanner := &mockRowScanner{rows: nil}
	events, err := scanRollbackEvents(scanner)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestScanRollbackEvents_SingleRow_AllFieldsPopulated(t *testing.T) {
	tokenID := uuid.New()
	walletID := "wallet-1"
	orgID := "org-1"

	scanner := &mockRowScanner{
		rows: []mockRow{
			makeRollbackRow(tokenID, "addr1", "5000", 150, "tx-150", walletID, orgID, model.ActivityDeposit, true),
		},
	}

	events, err := scanRollbackEvents(scanner)
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.Equal(t, tokenID, events[0].TokenID)
	assert.Equal(t, "addr1", events[0].Address)
	assert.Equal(t, "5000", events[0].Delta)
	assert.Equal(t, int64(150), events[0].BlockCursor)
	assert.Equal(t, "tx-150", events[0].TxHash)
	require.NotNil(t, events[0].WalletID)
	assert.Equal(t, walletID, *events[0].WalletID)
	require.NotNil(t, events[0].OrganizationID)
	assert.Equal(t, orgID, *events[0].OrganizationID)
	assert.Equal(t, model.ActivityDeposit, events[0].ActivityType)
	assert.True(t, events[0].BalanceApplied)
}

func TestScanRollbackEvents_NullWalletAndOrg(t *testing.T) {
	tokenID := uuid.New()

	scanner := &mockRowScanner{
		rows: []mockRow{
			makeRollbackRow(tokenID, "addr1", "-3000", 120, "tx-120", nil, nil, model.ActivityWithdrawal, true),
		},
	}

	events, err := scanRollbackEvents(scanner)
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.Nil(t, events[0].WalletID)
	assert.Nil(t, events[0].OrganizationID)
	assert.Equal(t, model.ActivityWithdrawal, events[0].ActivityType)
}

func TestScanRollbackEvents_MultiRow_MixedTypes(t *testing.T) {
	tokenID1 := uuid.New()
	tokenID2 := uuid.New()
	tokenID3 := uuid.New()
	walletID := "wallet-1"
	orgID := "org-1"

	scanner := &mockRowScanner{
		rows: []mockRow{
			makeRollbackRow(tokenID1, "addr1", "10000", 200, "tx-200", walletID, orgID, model.ActivityDeposit, true),
			makeRollbackRow(tokenID2, "addr2", "-5000", 199, "tx-199", nil, nil, model.ActivityStake, true),
			makeRollbackRow(tokenID3, "addr3", "2000", 198, "tx-198", walletID, nil, model.ActivityUnstake, true),
			makeRollbackRow(tokenID1, "addr1", "3000", 197, "tx-197", nil, orgID, model.ActivityDeposit, false),
			makeRollbackRow(tokenID2, "addr4", "-100", 196, "tx-196", nil, nil, model.ActivityFee, true),
		},
	}

	events, err := scanRollbackEvents(scanner)
	require.NoError(t, err)
	require.Len(t, events, 5)

	// Row 1: deposit, applied, wallet+org
	assert.Equal(t, tokenID1, events[0].TokenID)
	assert.Equal(t, "10000", events[0].Delta)
	assert.Equal(t, model.ActivityDeposit, events[0].ActivityType)
	assert.True(t, events[0].BalanceApplied)
	require.NotNil(t, events[0].WalletID)
	require.NotNil(t, events[0].OrganizationID)

	// Row 2: stake, applied, null wallet+org
	assert.Equal(t, tokenID2, events[1].TokenID)
	assert.Equal(t, "-5000", events[1].Delta)
	assert.Equal(t, model.ActivityStake, events[1].ActivityType)
	assert.Nil(t, events[1].WalletID)
	assert.Nil(t, events[1].OrganizationID)

	// Row 3: unstake, applied, wallet only (no org)
	assert.Equal(t, tokenID3, events[2].TokenID)
	assert.Equal(t, model.ActivityUnstake, events[2].ActivityType)
	require.NotNil(t, events[2].WalletID)
	assert.Nil(t, events[2].OrganizationID)

	// Row 4: deposit, NOT applied, org only (no wallet)
	assert.False(t, events[3].BalanceApplied)
	assert.Nil(t, events[3].WalletID)
	require.NotNil(t, events[3].OrganizationID)

	// Row 5: fee, applied
	assert.Equal(t, model.ActivityFee, events[4].ActivityType)
	assert.Equal(t, "-100", events[4].Delta)
}

func TestScanRollbackEvents_LargeBatch(t *testing.T) {
	const batchSize = 100
	rows := make([]mockRow, batchSize)
	for i := range rows {
		rows[i] = makeRollbackRow(
			uuid.New(), "addr1", "1000", int64(1000-i), "tx",
			nil, nil, model.ActivityDeposit, true,
		)
	}

	scanner := &mockRowScanner{rows: rows}
	events, err := scanRollbackEvents(scanner)
	require.NoError(t, err)
	assert.Len(t, events, batchSize)
}

func TestScanRollbackEvents_ScanError_PropagatesImmediately(t *testing.T) {
	scanner := &mockRowScanner{
		rows:    []mockRow{{values: make([]any, 9)}}, // dummy row to trigger Next()
		scanErr: errors.New("column type mismatch"),
	}

	events, err := scanRollbackEvents(scanner)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan rollback event")
	assert.Contains(t, err.Error(), "column type mismatch")
	assert.Nil(t, events)
}

func TestScanRollbackEvents_RowsErr_PropagatesAfterIteration(t *testing.T) {
	scanner := &mockRowScanner{
		rows:    nil, // no rows, but Err() returns error
		iterErr: errors.New("connection reset"),
	}

	events, err := scanRollbackEvents(scanner)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read rollback event rows")
	assert.Contains(t, err.Error(), "connection reset")
	assert.Nil(t, events)
}

func TestScanRollbackEvents_MultiRow_ThenBuildReversalItems_E2E(t *testing.T) {
	tokenID := uuid.New()
	walletID := "wallet-e2e"

	scanner := &mockRowScanner{
		rows: []mockRow{
			makeRollbackRow(tokenID, "addr1", "10000", 200, "tx-200", walletID, nil, model.ActivityDeposit, true),
			makeRollbackRow(tokenID, "addr1", "-5000", 201, "tx-201", walletID, nil, model.ActivityStake, true),
			makeRollbackRow(tokenID, "addr1", "3000", 202, "tx-202", nil, nil, model.ActivityDeposit, false),
		},
	}

	events, err := scanRollbackEvents(scanner)
	require.NoError(t, err)
	require.Len(t, events, 3)

	items, err := buildReversalAdjustItems(events)
	require.NoError(t, err)
	// applied deposit(1) + applied stake(2) + not-applied(0) = 3
	require.Len(t, items, 3)

	assert.Equal(t, "-10000", items[0].Delta)
	assert.Equal(t, "", items[0].BalanceType)
	require.NotNil(t, items[0].WalletID)

	assert.Equal(t, "5000", items[1].Delta)
	assert.Equal(t, "", items[1].BalanceType)

	assert.Equal(t, "-5000", items[2].Delta)
	assert.Equal(t, "staked", items[2].BalanceType)
}

// ---------------------------------------------------------------------------
// Tests: scanPromotedEvents
// ---------------------------------------------------------------------------

func TestScanPromotedEvents_Empty(t *testing.T) {
	scanner := &mockRowScanner{rows: nil}
	events, err := scanPromotedEvents(scanner)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestScanPromotedEvents_SingleRow_AllFieldsPopulated(t *testing.T) {
	tokenID := uuid.New()
	walletID := "wallet-promo"
	orgID := "org-promo"

	scanner := &mockRowScanner{
		rows: []mockRow{
			makePromotedRow(tokenID, "addr-p1", "3000", 100, "tx-p100", walletID, orgID, model.ActivityDeposit),
		},
	}

	events, err := scanPromotedEvents(scanner)
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.Equal(t, tokenID, events[0].TokenID)
	assert.Equal(t, "addr-p1", events[0].Address)
	assert.Equal(t, "3000", events[0].Delta)
	assert.Equal(t, int64(100), events[0].BlockCursor)
	assert.Equal(t, "tx-p100", events[0].TxHash)
	require.NotNil(t, events[0].WalletID)
	assert.Equal(t, walletID, *events[0].WalletID)
	require.NotNil(t, events[0].OrganizationID)
	assert.Equal(t, orgID, *events[0].OrganizationID)
	assert.Equal(t, model.ActivityDeposit, events[0].ActivityType)
}

func TestScanPromotedEvents_NullWalletAndOrg(t *testing.T) {
	tokenID := uuid.New()

	scanner := &mockRowScanner{
		rows: []mockRow{
			makePromotedRow(tokenID, "addr1", "-1000", 101, "tx-101", nil, nil, model.ActivityWithdrawal),
		},
	}

	events, err := scanPromotedEvents(scanner)
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.Nil(t, events[0].WalletID)
	assert.Nil(t, events[0].OrganizationID)
}

func TestScanPromotedEvents_MultiRow_MixedTypes(t *testing.T) {
	tokenID1 := uuid.New()
	tokenID2 := uuid.New()
	walletID := "wallet-1"

	scanner := &mockRowScanner{
		rows: []mockRow{
			makePromotedRow(tokenID1, "addr1", "3000", 100, "tx-100", walletID, nil, model.ActivityDeposit),
			makePromotedRow(tokenID2, "addr2", "-1000", 101, "tx-101", nil, nil, model.ActivityWithdrawal),
			makePromotedRow(tokenID1, "addr1", "-5000", 102, "tx-102", walletID, nil, model.ActivityStake),
			makePromotedRow(tokenID1, "addr1", "2000", 103, "tx-103", nil, nil, model.ActivityUnstake),
		},
	}

	events, err := scanPromotedEvents(scanner)
	require.NoError(t, err)
	require.Len(t, events, 4)

	assert.Equal(t, model.ActivityDeposit, events[0].ActivityType)
	assert.Equal(t, model.ActivityWithdrawal, events[1].ActivityType)
	assert.Equal(t, model.ActivityStake, events[2].ActivityType)
	assert.Equal(t, model.ActivityUnstake, events[3].ActivityType)

	require.NotNil(t, events[0].WalletID)
	assert.Nil(t, events[1].WalletID)
	require.NotNil(t, events[2].WalletID)
	assert.Nil(t, events[3].WalletID)
}

func TestScanPromotedEvents_LargeBatch(t *testing.T) {
	const batchSize = 100
	rows := make([]mockRow, batchSize)
	for i := range rows {
		rows[i] = makePromotedRow(
			uuid.New(), "addr1", "500", int64(i), "tx",
			nil, nil, model.ActivityDeposit,
		)
	}

	scanner := &mockRowScanner{rows: rows}
	events, err := scanPromotedEvents(scanner)
	require.NoError(t, err)
	assert.Len(t, events, batchSize)
}

func TestScanPromotedEvents_ScanError_PropagatesImmediately(t *testing.T) {
	scanner := &mockRowScanner{
		rows:    []mockRow{{values: make([]any, 8)}},
		scanErr: errors.New("invalid uuid format"),
	}

	events, err := scanPromotedEvents(scanner)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan promoted event")
	assert.Contains(t, err.Error(), "invalid uuid format")
	assert.Nil(t, events)
}

func TestScanPromotedEvents_RowsErr_PropagatesAfterIteration(t *testing.T) {
	scanner := &mockRowScanner{
		rows:    nil,
		iterErr: errors.New("network timeout"),
	}

	events, err := scanPromotedEvents(scanner)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read promoted event rows")
	assert.Contains(t, err.Error(), "network timeout")
	assert.Nil(t, events)
}

func TestScanPromotedEvents_MultiRow_ThenBuildPromotionItems_E2E(t *testing.T) {
	tokenID := uuid.New()

	scanner := &mockRowScanner{
		rows: []mockRow{
			makePromotedRow(tokenID, "addr1", "1000", 50, "tx-50", nil, nil, model.ActivityDeposit),
			makePromotedRow(tokenID, "addr1", "-3000", 51, "tx-51", nil, nil, model.ActivityStake),
			makePromotedRow(tokenID, "addr1", "2000", 52, "tx-52", nil, nil, model.ActivityUnstake),
		},
	}

	events, err := scanPromotedEvents(scanner)
	require.NoError(t, err)
	require.Len(t, events, 3)

	items, err := buildPromotionAdjustItems(events)
	require.NoError(t, err)
	// deposit(1) + stake(2) + unstake(2) = 5
	require.Len(t, items, 5)

	assert.Equal(t, "1000", items[0].Delta)
	assert.Equal(t, "", items[0].BalanceType)

	assert.Equal(t, "-3000", items[1].Delta)
	assert.Equal(t, "", items[1].BalanceType)

	assert.Equal(t, "3000", items[2].Delta)
	assert.Equal(t, "staked", items[2].BalanceType)

	assert.Equal(t, "2000", items[3].Delta)
	assert.Equal(t, "", items[3].BalanceType)

	assert.Equal(t, "-2000", items[4].Delta)
	assert.Equal(t, "staked", items[4].BalanceType)
}

// ---------------------------------------------------------------------------
// Test: scanRollbackEvents with partial success then error (multi-row scan error)
// ---------------------------------------------------------------------------

func TestScanRollbackEvents_PartialRows_ThenScanError(t *testing.T) {
	tokenID := uuid.New()

	// First row succeeds, second row triggers scan error.
	scanner := &mockRowScanner{
		rows: []mockRow{
			makeRollbackRow(tokenID, "addr1", "1000", 100, "tx-100", nil, nil, model.ActivityDeposit, true),
			{values: make([]any, 9)}, // will trigger scanErr on second iteration
		},
	}

	// After first row succeeds, inject error for second.
	// We need a custom scanner for this.
	countingScanner := &countingScanRowScanner{
		rows:         scanner.rows,
		failOnScanAt: 1, // fail on second Scan call
		scanErr:      errors.New("corrupted row data"),
	}

	events, err := scanRollbackEvents(countingScanner)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan rollback event")
	assert.Nil(t, events)
}

// countingScanRowScanner fails Scan() on a specific call index.
type countingScanRowScanner struct {
	rows         []mockRow
	idx          int
	scanCount    int
	failOnScanAt int
	scanErr      error
}

func (m *countingScanRowScanner) Next() bool {
	return m.idx < len(m.rows)
}

func (m *countingScanRowScanner) Scan(dest ...any) error {
	if m.scanCount == m.failOnScanAt {
		return m.scanErr
	}
	row := m.rows[m.idx]
	m.idx++
	m.scanCount++
	for i, val := range row.values {
		if i >= len(dest) {
			break
		}
		switch d := dest[i].(type) {
		case *uuid.UUID:
			*d = val.(uuid.UUID)
		case *string:
			*d = val.(string)
		case *int64:
			*d = val.(int64)
		case *bool:
			*d = val.(bool)
		case *model.ActivityType:
			*d = val.(model.ActivityType)
		case *sql.NullString:
			if val == nil {
				d.Valid = false
			} else {
				d.Valid = true
				d.String = val.(string)
			}
		}
	}
	return nil
}

func (m *countingScanRowScanner) Err() error { return nil }
