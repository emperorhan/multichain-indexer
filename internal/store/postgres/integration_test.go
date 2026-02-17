//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDB(t *testing.T) *postgres.DB {
	t.Helper()
	url := os.Getenv("TEST_DB_URL")
	if url == "" {
		url = "postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable"
	}
	db, err := postgres.New(postgres.Config{
		URL:             url,
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
	})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// ---------- CursorRepo ----------

func TestCursorRepo_EnsureExistsAndGet(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewCursorRepo(db)
	ctx := context.Background()
	addr := "test-cursor-" + uuid.NewString()[:8]

	// Get returns nil for non-existent cursor.
	cursor, err := repo.Get(ctx, model.ChainSolana, model.NetworkDevnet, addr)
	require.NoError(t, err)
	assert.Nil(t, cursor)

	// EnsureExists creates cursor row.
	require.NoError(t, repo.EnsureExists(ctx, model.ChainSolana, model.NetworkDevnet, addr))

	cursor, err = repo.Get(ctx, model.ChainSolana, model.NetworkDevnet, addr)
	require.NoError(t, err)
	require.NotNil(t, cursor)
	assert.Equal(t, model.ChainSolana, cursor.Chain)
	assert.Equal(t, model.NetworkDevnet, cursor.Network)
	assert.Equal(t, addr, cursor.Address)
	assert.Equal(t, int64(0), cursor.CursorSequence)
}

func TestCursorRepo_UpsertTx(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewCursorRepo(db)
	ctx := context.Background()
	addr := "test-cursor-upsert-" + uuid.NewString()[:8]
	cursorVal := "sig-abc123"

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	require.NoError(t, repo.UpsertTx(ctx, tx, model.ChainSolana, model.NetworkDevnet, addr, &cursorVal, 100, 5))
	require.NoError(t, tx.Commit())

	cursor, err := repo.Get(ctx, model.ChainSolana, model.NetworkDevnet, addr)
	require.NoError(t, err)
	require.NotNil(t, cursor)
	assert.Equal(t, int64(100), cursor.CursorSequence)
	assert.Equal(t, cursorVal, *cursor.CursorValue)
	assert.Equal(t, int64(5), cursor.ItemsProcessed)

	// Second upsert accumulates items_processed.
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	cursorVal2 := "sig-def456"
	require.NoError(t, repo.UpsertTx(ctx, tx2, model.ChainSolana, model.NetworkDevnet, addr, &cursorVal2, 200, 3))
	require.NoError(t, tx2.Commit())

	cursor, err = repo.Get(ctx, model.ChainSolana, model.NetworkDevnet, addr)
	require.NoError(t, err)
	assert.Equal(t, int64(200), cursor.CursorSequence)
	assert.Equal(t, cursorVal2, *cursor.CursorValue)
	assert.Equal(t, int64(8), cursor.ItemsProcessed) // 5 + 3
}

// ---------- TokenRepo ----------

func TestTokenRepo_UpsertAndFind(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewTokenRepo(db)
	ctx := context.Background()
	contract := "test-token-" + uuid.NewString()[:8]

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	tokenID, err := repo.UpsertTx(ctx, tx, &model.Token{
		Chain:           model.ChainSolana,
		Network:         model.NetworkDevnet,
		ContractAddress: contract,
		Symbol:          "TST",
		Name:            "Test Token",
		Decimals:        9,
		TokenType:       model.TokenTypeFungible,
		ChainData:       json.RawMessage("{}"),
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	assert.NotEqual(t, uuid.Nil, tokenID)

	// FindByContractAddress
	found, err := repo.FindByContractAddress(ctx, model.ChainSolana, model.NetworkDevnet, contract)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, tokenID, found.ID)
	assert.Equal(t, "TST", found.Symbol)
	assert.Equal(t, int32(9), found.Decimals)

	// Idempotent upsert returns same ID.
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	tokenID2, err := repo.UpsertTx(ctx, tx2, &model.Token{
		Chain:           model.ChainSolana,
		Network:         model.NetworkDevnet,
		ContractAddress: contract,
		Symbol:          "TST",
		Name:            "Test Token",
		Decimals:        9,
		TokenType:       model.TokenTypeFungible,
		ChainData:       json.RawMessage("{}"),
	})
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())
	assert.Equal(t, tokenID, tokenID2)
}

// ---------- TransactionRepo ----------

func TestTransactionRepo_UpsertIdempotent(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewTransactionRepo(db)
	ctx := context.Background()
	txHash := "test-tx-" + uuid.NewString()[:8]

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	id1, err := repo.UpsertTx(ctx, tx, &model.Transaction{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		TxHash:    txHash,
		FeeAmount: "5000",
		FeePayer:  "payer1",
		Status:    model.TxStatusSuccess,
		ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	assert.NotEqual(t, uuid.Nil, id1)

	// Second upsert returns same ID.
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	id2, err := repo.UpsertTx(ctx, tx2, &model.Transaction{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		TxHash:    txHash,
		FeeAmount: "5000",
		FeePayer:  "payer1",
		Status:    model.TxStatusSuccess,
		ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())
	assert.Equal(t, id1, id2)
}

// ---------- BalanceRepo ----------

func TestBalanceRepo_AdjustAndGet(t *testing.T) {
	db := testDB(t)
	tokenRepo := postgres.NewTokenRepo(db)
	balanceRepo := postgres.NewBalanceRepo(db)
	ctx := context.Background()
	addr := "test-balance-" + uuid.NewString()[:8]
	contract := "test-balance-token-" + uuid.NewString()[:8]

	// Create a token first.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	tokenID, err := tokenRepo.UpsertTx(ctx, tx, &model.Token{
		Chain:           model.ChainSolana,
		Network:         model.NetworkDevnet,
		ContractAddress: contract,
		Symbol:          "SOL",
		Name:            "Solana",
		Decimals:        9,
		TokenType:       model.TokenTypeNative,
		ChainData:       json.RawMessage("{}"),
	})
	require.NoError(t, err)

	// GetAmountTx returns "0" for non-existent balance.
	amount, err := balanceRepo.GetAmountTx(ctx, tx, model.ChainSolana, model.NetworkDevnet, addr, tokenID)
	require.NoError(t, err)
	assert.Equal(t, "0", amount)

	// Deposit +1000000000
	walletID := "wallet-1"
	orgID := "org-1"
	require.NoError(t, balanceRepo.AdjustBalanceTx(ctx, tx,
		model.ChainSolana, model.NetworkDevnet, addr,
		tokenID, &walletID, &orgID, "1000000000", 100, "tx-deposit"))

	amount, err = balanceRepo.GetAmountTx(ctx, tx, model.ChainSolana, model.NetworkDevnet, addr, tokenID)
	require.NoError(t, err)
	assert.Equal(t, "1000000000", amount)

	// Withdraw -500000000
	require.NoError(t, balanceRepo.AdjustBalanceTx(ctx, tx,
		model.ChainSolana, model.NetworkDevnet, addr,
		tokenID, &walletID, &orgID, "-500000000", 101, "tx-withdraw"))

	amount, err = balanceRepo.GetAmountTx(ctx, tx, model.ChainSolana, model.NetworkDevnet, addr, tokenID)
	require.NoError(t, err)
	assert.Equal(t, "500000000", amount)

	require.NoError(t, tx.Commit())

	// GetByAddress
	balances, err := balanceRepo.GetByAddress(ctx, model.ChainSolana, model.NetworkDevnet, addr)
	require.NoError(t, err)
	require.Len(t, balances, 1)
	assert.Equal(t, "500000000", balances[0].Amount)
}

// ---------- BalanceEventRepo ----------

func TestBalanceEventRepo_UpsertIdempotent(t *testing.T) {
	db := testDB(t)
	txRepo := postgres.NewTransactionRepo(db)
	tokenRepo := postgres.NewTokenRepo(db)
	beRepo := postgres.NewBalanceEventRepo(db)
	ctx := context.Background()
	txHash := "test-be-tx-" + uuid.NewString()[:8]
	contract := "test-be-token-" + uuid.NewString()[:8]
	eventID := "evt-" + uuid.NewString()[:8]
	watchedAddr := "test-be-watched-" + uuid.NewString()[:8]

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	txID, err := txRepo.UpsertTx(ctx, tx, &model.Transaction{
		Chain:     model.ChainSolana,
		Network:   model.NetworkDevnet,
		TxHash:    txHash,
		FeeAmount: "5000",
		FeePayer:  "payer1",
		Status:    model.TxStatusSuccess,
		ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)

	tokenID, err := tokenRepo.UpsertTx(ctx, tx, &model.Token{
		Chain:           model.ChainSolana,
		Network:         model.NetworkDevnet,
		ContractAddress: contract,
		Symbol:          "SOL",
		Name:            "Solana",
		Decimals:        9,
		TokenType:       model.TokenTypeNative,
		ChainData:       json.RawMessage("{}"),
	})
	require.NoError(t, err)

	be := &model.BalanceEvent{
		Chain:           model.ChainSolana,
		Network:         model.NetworkDevnet,
		TransactionID:   txID,
		TxHash:          txHash,
		TokenID:         tokenID,
		EventCategory:   "transfer",
		EventAction:     "deposit",
		Address:         watchedAddr,
		Delta:           "1000000",
		WatchedAddress:  &watchedAddr,
		BlockCursor:     100,
		ChainData:       json.RawMessage("{}"),
		EventID:         eventID,
		FinalityState:   "confirmed",
		BalanceBefore:   strPtr("0"),
		BalanceAfter:    strPtr("1000000"),
		DecoderVersion:  "v1",
		SchemaVersion:   "v1",
	}

	inserted, err := beRepo.UpsertTx(ctx, tx, be)
	require.NoError(t, err)
	assert.True(t, inserted, "first insert should return true")

	// Same event_id with same finality → no update (WHERE clause rejects equal rank).
	inserted2, err := beRepo.UpsertTx(ctx, tx, be)
	require.NoError(t, err)
	assert.False(t, inserted2, "duplicate insert with same finality should return false")

	// Same event_id with higher finality → update succeeds.
	be.FinalityState = "finalized"
	inserted3, err := beRepo.UpsertTx(ctx, tx, be)
	require.NoError(t, err)
	assert.False(t, inserted3, "finality upgrade should update but return false (xmax != 0)")

	require.NoError(t, tx.Commit())
}

func TestBalanceEventRepo_RequiresEventID(t *testing.T) {
	db := testDB(t)
	beRepo := postgres.NewBalanceEventRepo(db)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()

	_, err = beRepo.UpsertTx(ctx, tx, &model.BalanceEvent{
		EventID: "", // empty
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event_id is required")
}

func strPtr(s string) *string { return &s }
