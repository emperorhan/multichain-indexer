package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Fake driver infrastructure (per-test isolation)
// ---------------------------------------------------------------------------

var beqDriverSeq atomic.Int64

type beqQueryHandler func(query string, args []driver.Value) (driver.Rows, error)

type beqFakeDriver struct{ conn *beqFakeConn }
type beqFakeConn struct {
	queryHandler beqQueryHandler
}
type beqFakeTx struct{}

func (d *beqFakeDriver) Open(string) (driver.Conn, error) { return d.conn, nil }
func (c *beqFakeConn) Prepare(query string) (driver.Stmt, error) {
	return &beqFakeStmt{conn: c, query: query}, nil
}
func (c *beqFakeConn) Close() error              { return nil }
func (c *beqFakeConn) Begin() (driver.Tx, error) { return &beqFakeTx{}, nil }
func (tx *beqFakeTx) Commit() error              { return nil }
func (tx *beqFakeTx) Rollback() error            { return nil }

type beqFakeStmt struct {
	conn  *beqFakeConn
	query string
}

func (s *beqFakeStmt) Close() error  { return nil }
func (s *beqFakeStmt) NumInput() int { return -1 }
func (s *beqFakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (s *beqFakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	if s.conn.queryHandler != nil {
		return s.conn.queryHandler(s.query, args)
	}
	return &beqEmptyRows{}, nil
}

type beqEmptyRows struct{}

func (r *beqEmptyRows) Columns() []string { return beBlockRangeColumns }
func (r *beqEmptyRows) Close() error      { return nil }
func (r *beqEmptyRows) Next([]driver.Value) error {
	return io.EOF
}

type beqDataRows struct {
	columns []string
	data    [][]driver.Value
	idx     int
}

func (r *beqDataRows) Columns() []string { return r.columns }
func (r *beqDataRows) Close() error      { return nil }
func (r *beqDataRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.idx])
	r.idx++
	return nil
}

var beBlockRangeColumns = []string{
	"id", "event_id", "chain", "network", "tx_hash",
	"block_cursor", "block_hash", "block_time", "tx_index",
	"activity_type", "event_action", "event_path", "event_path_type",
	"program_id", "address", "actor_address", "counterparty_address",
	"asset_type", "asset_id", "delta", "balance_applied",
	"finality_state", "decoder_version", "schema_version", "chain_data",
	"watched_address", "wallet_id", "organization_id",
}

func openBEQFakeDB(t *testing.T, handler beqQueryHandler) *DB {
	t.Helper()
	name := fmt.Sprintf("fake_beq_%d", beqDriverSeq.Add(1))
	conn := &beqFakeConn{queryHandler: handler}
	sql.Register(name, &beqFakeDriver{conn: conn})
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &DB{db}
}

// ---------------------------------------------------------------------------
// Tests: GetByBlockRange
// ---------------------------------------------------------------------------

func TestGetByBlockRange_EmptyResult(t *testing.T) {
	db := openBEQFakeDB(t, nil) // nil handler → empty rows
	repo := NewBalanceEventRepo(db)

	events, err := repo.GetByBlockRange(context.Background(), "ethereum", "mainnet", 100, 200)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestGetByBlockRange_SingleRow(t *testing.T) {
	id := uuid.New()
	bt := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	watchedAddr := "0xWatched"
	walletID := "wallet-1"
	orgID := "org-1"

	handler := func(query string, args []driver.Value) (driver.Rows, error) {
		if !strings.Contains(query, "balance_events") {
			return &beqEmptyRows{}, nil
		}
		// Verify args: chain, network, startBlock, endBlock.
		assert.Equal(t, "ethereum", args[0])
		assert.Equal(t, "mainnet", args[1])
		assert.Equal(t, int64(100), args[2])
		assert.Equal(t, int64(200), args[3])

		return &beqDataRows{
			columns: beBlockRangeColumns,
			data: [][]driver.Value{
				{
					id.String(),   // id (UUID as string)
					"evt-1",       // event_id
					"ethereum",    // chain
					"mainnet",     // network
					"0xTxHash",    // tx_hash
					int64(150),    // block_cursor
					"0xBlockHash", // block_hash
					bt,            // block_time
					int64(0),      // tx_index
					"DEPOSIT",     // activity_type
					"erc20_transfer", // event_action
					"log:0",       // event_path
					"base_log",    // event_path_type
					"0xProgram",   // program_id
					"0xAddr",      // address
					"0xActor",     // actor_address
					"0xCounter",   // counterparty_address
					"fungible",    // asset_type
					"ETH",         // asset_id
					"1000000",     // delta
					true,          // balance_applied
					"confirmed",   // finality_state
					"1.0.0",       // decoder_version
					"1",           // schema_version
					json.RawMessage(`{}`), // chain_data
					watchedAddr,   // watched_address
					walletID,      // wallet_id
					orgID,         // organization_id
				},
			},
		}, nil
	}

	db := openBEQFakeDB(t, handler)
	repo := NewBalanceEventRepo(db)

	events, err := repo.GetByBlockRange(context.Background(), "ethereum", "mainnet", 100, 200)
	require.NoError(t, err)
	require.Len(t, events, 1)

	ev := events[0]
	assert.Equal(t, "evt-1", ev.EventID)
	assert.Equal(t, "ethereum", string(ev.Chain))
	assert.Equal(t, "mainnet", string(ev.Network))
	assert.Equal(t, "0xTxHash", ev.TxHash)
	assert.Equal(t, int64(150), ev.BlockCursor)
	assert.Equal(t, "0xBlockHash", ev.BlockHash)
	require.NotNil(t, ev.BlockTime)
	assert.Equal(t, bt, *ev.BlockTime)
	assert.Equal(t, "DEPOSIT", string(ev.ActivityType))
	assert.Equal(t, "erc20_transfer", ev.EventAction)
	assert.Equal(t, "log:0", ev.EventPath)
	assert.Equal(t, "base_log", ev.EventPathType)
	assert.Equal(t, "0xProgram", ev.ProgramID)
	assert.Equal(t, "0xAddr", ev.Address)
	assert.Equal(t, "0xActor", ev.ActorAddress)
	assert.Equal(t, "0xCounter", ev.CounterpartyAddress)
	assert.Equal(t, "fungible", ev.AssetType)
	assert.Equal(t, "ETH", ev.AssetID)
	assert.Equal(t, "1000000", ev.Delta)
	assert.True(t, ev.BalanceApplied)
	assert.Equal(t, "confirmed", ev.FinalityState)
	assert.Equal(t, "1.0.0", ev.DecoderVersion)
	assert.Equal(t, "1", ev.SchemaVersion)
	require.NotNil(t, ev.WatchedAddress)
	assert.Equal(t, watchedAddr, *ev.WatchedAddress)
	require.NotNil(t, ev.WalletID)
	assert.Equal(t, walletID, *ev.WalletID)
	require.NotNil(t, ev.OrganizationID)
	assert.Equal(t, orgID, *ev.OrganizationID)
}

func TestGetByBlockRange_MultipleRows(t *testing.T) {
	id1 := uuid.New()
	id2 := uuid.New()
	bt := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	handler := func(query string, _ []driver.Value) (driver.Rows, error) {
		if !strings.Contains(query, "balance_events") {
			return &beqEmptyRows{}, nil
		}
		return &beqDataRows{
			columns: beBlockRangeColumns,
			data: [][]driver.Value{
				{
					id1.String(), "evt-1", "ethereum", "mainnet", "0xTx1",
					int64(100), "0xHash1", bt, int64(0),
					"DEPOSIT", "erc20_transfer", "log:0", "base_log",
					"0xProg", "0xAddr1", "0xActor1", "0xCounter1",
					"fungible", "ETH", "1000", true,
					"confirmed", "1.0.0", "1", json.RawMessage(`{}`),
					nil, nil, nil, // nullable fields
				},
				{
					id2.String(), "evt-2", "ethereum", "mainnet", "0xTx2",
					int64(101), "0xHash2", bt, int64(1),
					"WITHDRAWAL", "erc20_transfer", "log:1", "base_log",
					"0xProg", "0xAddr2", "0xActor2", "",
					"fungible", "USDT", "-500", true,
					"finalized", "1.0.0", "1", json.RawMessage(`{}`),
					nil, nil, nil,
				},
			},
		}, nil
	}

	db := openBEQFakeDB(t, handler)
	repo := NewBalanceEventRepo(db)

	events, err := repo.GetByBlockRange(context.Background(), "ethereum", "mainnet", 100, 200)
	require.NoError(t, err)
	require.Len(t, events, 2)

	assert.Equal(t, "evt-1", events[0].EventID)
	assert.Equal(t, "evt-2", events[1].EventID)
	assert.Equal(t, "1000", events[0].Delta)
	assert.Equal(t, "-500", events[1].Delta)
}

func TestGetByBlockRange_QueryError(t *testing.T) {
	handler := func(query string, _ []driver.Value) (driver.Rows, error) {
		return nil, fmt.Errorf("connection refused")
	}

	db := openBEQFakeDB(t, handler)
	repo := NewBalanceEventRepo(db)

	events, err := repo.GetByBlockRange(context.Background(), "solana", "devnet", 1, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get by block range")
	assert.Nil(t, events)
}

func TestGetByBlockRange_NullableFields(t *testing.T) {
	id := uuid.New()
	bt := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	handler := func(query string, _ []driver.Value) (driver.Rows, error) {
		if !strings.Contains(query, "balance_events") {
			return &beqEmptyRows{}, nil
		}
		return &beqDataRows{
			columns: beBlockRangeColumns,
			data: [][]driver.Value{
				{
					id.String(), "evt-null", "btc", "mainnet", "txhash",
					int64(800000), "blockhash", bt, int64(0),
					"DEPOSIT", "utxo_receive", "vout:0", "btc_utxo",
					"", "bc1qwatched", "bc1qactor", "",
					"native", "BTC", "50000", false,
					"confirmed", "1.0.0", "1", json.RawMessage(`{}`),
					nil, nil, nil, // watched_address, wallet_id, org_id all NULL
				},
			},
		}, nil
	}

	db := openBEQFakeDB(t, handler)
	repo := NewBalanceEventRepo(db)

	events, err := repo.GetByBlockRange(context.Background(), "btc", "mainnet", 800000, 800010)
	require.NoError(t, err)
	require.Len(t, events, 1)

	ev := events[0]
	assert.Nil(t, ev.WatchedAddress)
	assert.Nil(t, ev.WalletID)
	assert.Nil(t, ev.OrganizationID)
	assert.False(t, ev.BalanceApplied)
}

func TestGetByBlockRange_QueryContainsExpectedClauses(t *testing.T) {
	var capturedQuery string
	var capturedArgs []driver.Value

	handler := func(query string, args []driver.Value) (driver.Rows, error) {
		capturedQuery = query
		capturedArgs = args
		return &beqEmptyRows{}, nil
	}

	db := openBEQFakeDB(t, handler)
	repo := NewBalanceEventRepo(db)

	_, err := repo.GetByBlockRange(context.Background(), "solana", "devnet", 300000000, 300000010)
	require.NoError(t, err)

	assert.Contains(t, capturedQuery, "FROM balance_events")
	assert.Contains(t, capturedQuery, "WHERE chain = $1")
	assert.Contains(t, capturedQuery, "block_cursor >= $3")
	assert.Contains(t, capturedQuery, "block_cursor <= $4")
	assert.Contains(t, capturedQuery, "ORDER BY block_cursor, tx_index, event_path")

	require.Len(t, capturedArgs, 4)
	assert.Equal(t, "solana", capturedArgs[0])
	assert.Equal(t, "devnet", capturedArgs[1])
	assert.Equal(t, int64(300000000), capturedArgs[2])
	assert.Equal(t, int64(300000010), capturedArgs[3])
}

func TestGetByBlockRange_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	db := openBEQFakeDB(t, nil)
	repo := NewBalanceEventRepo(db)

	_, err := repo.GetByBlockRange(ctx, "ethereum", "mainnet", 1, 10)
	assert.Error(t, err)
}
