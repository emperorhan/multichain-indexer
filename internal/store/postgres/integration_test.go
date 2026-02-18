//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
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

func TestBalanceEventRepo_ReplayStableCanonicalIDAcrossBlockTimeShift(t *testing.T) {
	db := testDB(t)
	txRepo := postgres.NewTransactionRepo(db)
	tokenRepo := postgres.NewTokenRepo(db)
	beRepo := postgres.NewBalanceEventRepo(db)
	ctx := context.Background()

	testCases := []struct {
		name    string
		chain   model.Chain
		network model.Network
	}{
		{name: "solana-devnet", chain: model.ChainSolana, network: model.NetworkDevnet},
		{name: "base-sepolia", chain: model.ChainBase, network: model.NetworkSepolia},
		{name: "btc-testnet", chain: model.ChainBTC, network: model.NetworkTestnet},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			contract := fmt.Sprintf("test-replay-token-%s-%s", tc.chain, tc.network) + "+" + uuid.NewString()[:8]
			txHash := fmt.Sprintf("test-replay-tx-%s-%s-%s", tc.chain, tc.network, uuid.NewString()[:8])
			eventID := fmt.Sprintf("replay-event-%s-%s-%s", tc.chain, tc.network, uuid.NewString()[:8])
			watchedAddr := fmt.Sprintf("test-replay-watched-%s-%s-%s", tc.chain, tc.network, uuid.NewString()[:8])

			// Setup shared tx and token.
			setupTx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)

			txID, err := txRepo.UpsertTx(ctx, setupTx, &model.Transaction{
				Chain:     tc.chain,
				Network:   tc.network,
				TxHash:    txHash,
				FeeAmount: "5000",
				FeePayer:  "payer1",
				Status:    model.TxStatusSuccess,
				ChainData: json.RawMessage("{}"),
			})
			require.NoError(t, err)

			tokenID, err := tokenRepo.UpsertTx(ctx, setupTx, &model.Token{
				Chain:           tc.chain,
				Network:         tc.network,
				ContractAddress: contract,
				Symbol:          "X",
				Name:            "Replay Token",
				Decimals:        9,
				TokenType:       model.TokenTypeNative,
				ChainData:       json.RawMessage("{}"),
			})
			require.NoError(t, err)
			require.NoError(t, setupTx.Commit())

			// First replay write on the month boundary to exercise partition drift.
			blockTimeBefore := time.Date(2026, 1, 31, 23, 55, 0, 0, time.UTC)
			event := &model.BalanceEvent{
				Chain:         tc.chain,
				Network:       tc.network,
				TransactionID: txID,
				TxHash:        txHash,
				TokenID:       tokenID,
				EventCategory: model.EventCategoryTransfer,
				EventAction:   "replay",
				Address:       watchedAddr,
				Delta:         "1000000",
				WatchedAddress: &watchedAddr,
				BlockCursor:   100,
				BlockTime:     &blockTimeBefore,
				ChainData:     json.RawMessage("{}"),
				EventID:       eventID,
				FinalityState: "confirmed",
				BalanceBefore:  strPtr("0"),
				BalanceAfter:   strPtr("1000000"),
				DecoderVersion: "v1",
				SchemaVersion:  "v1",
			}

			txA, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			insertedBefore, err := beRepo.UpsertTx(ctx, txA, event)
			require.NoError(t, err)
			require.NoError(t, txA.Commit())
			assert.True(t, insertedBefore)

			var upsertCountBefore int
			err = db.QueryRow("SELECT COUNT(*) FROM balance_events WHERE chain = $1 AND network = $2 AND event_id = $3", tc.chain, tc.network, eventID).Scan(&upsertCountBefore)
			require.NoError(t, err)
			assert.Equal(t, 1, upsertCountBefore)

			// Replay the same canonical event_id with shifted block_time to validate deterministic cursor and finality replay.
			blockTimeAfter := time.Date(2026, 2, 1, 0, 5, 0, 0, time.UTC)
			event.BlockTime = &blockTimeAfter
			event.FinalityState = "confirmed"
			event.BlockCursor = 101

			txB, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			insertedAfter, err := beRepo.UpsertTx(ctx, txB, event)
			require.NoError(t, err)
			require.NoError(t, txB.Commit())
			assert.False(t, insertedAfter)

			var storedState string
			var storedCursor int64
			var storedBlockTime time.Time
			err = db.QueryRow(
				"SELECT finality_state, block_cursor, block_time FROM balance_events WHERE chain = $1 AND network = $2 AND event_id = $3",
				tc.chain,
				tc.network,
				eventID,
			).Scan(&storedState, &storedCursor, &storedBlockTime)
			require.NoError(t, err)
			assert.Equal(t, "confirmed", storedState)
			assert.Equal(t, int64(101), storedCursor)
			assert.True(t, !storedBlockTime.Before(blockTimeAfter))

			// Replay the same canonical event_id with higher finality without re-creating the row.
			event.FinalityState = "finalized"
			event.BlockCursor = 102

			txC, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			insertedFinalized, err := beRepo.UpsertTx(ctx, txC, event)
			require.NoError(t, err)
			require.NoError(t, txC.Commit())
			assert.False(t, insertedFinalized)

			var upsertCountAfter int
			err = db.QueryRow("SELECT COUNT(*) FROM balance_events WHERE chain = $1 AND network = $2 AND event_id = $3", tc.chain, tc.network, eventID).Scan(&upsertCountAfter)
			require.NoError(t, err)
			assert.Equal(t, 1, upsertCountAfter)

			err = db.QueryRow(
				"SELECT finality_state, block_cursor, block_time FROM balance_events WHERE chain = $1 AND network = $2 AND event_id = $3",
				tc.chain,
				tc.network,
				eventID,
			).Scan(&storedState, &storedCursor, &storedBlockTime)
			require.NoError(t, err)
			assert.Equal(t, "finalized", storedState)
			assert.Equal(t, int64(102), storedCursor)
			assert.True(t, !storedBlockTime.Before(blockTimeAfter))
		})
	}
}

func strPtr(s string) *string { return &s }

// ---------- Extended Integration Tests ----------

func TestBalanceEventRepo_ConcurrentUpsertSameEventID(t *testing.T) {
	db := testDB(t)
	txRepo := postgres.NewTransactionRepo(db)
	tokenRepo := postgres.NewTokenRepo(db)
	beRepo := postgres.NewBalanceEventRepo(db)
	ctx := context.Background()
	txHash := "test-concurrent-be-tx-" + uuid.NewString()[:8]
	contract := "test-concurrent-be-token-" + uuid.NewString()[:8]
	eventID := "concurrent-evt-" + uuid.NewString()[:8]
	watchedAddr := "test-concurrent-be-" + uuid.NewString()[:8]

	// Setup: create transaction and token.
	setupTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	txID, err := txRepo.UpsertTx(ctx, setupTx, &model.Transaction{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		TxHash: txHash, FeeAmount: "5000", FeePayer: "payer1",
		Status: model.TxStatusSuccess, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)
	tokenID, err := tokenRepo.UpsertTx(ctx, setupTx, &model.Token{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		ContractAddress: contract, Symbol: "SOL", Name: "Solana",
		Decimals: 9, TokenType: model.TokenTypeNative, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)
	require.NoError(t, setupTx.Commit())

	// Concurrently upsert the same event_id from 10 goroutines.
	const goroutines = 10
	var wg sync.WaitGroup
	insertedCount := int32(0)
	errCount := int32(0)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gTx, gErr := db.BeginTx(ctx, nil)
			if gErr != nil {
				atomic.AddInt32(&errCount, 1)
				return
			}
			be := &model.BalanceEvent{
				Chain: model.ChainSolana, Network: model.NetworkDevnet,
				TransactionID: txID, TxHash: txHash, TokenID: tokenID,
				EventCategory: "transfer", EventAction: "deposit",
				Address: watchedAddr, Delta: "1000000",
				WatchedAddress: &watchedAddr, BlockCursor: 100,
				ChainData: json.RawMessage("{}"), EventID: eventID,
				FinalityState: "confirmed", BalanceBefore: strPtr("0"),
				BalanceAfter: strPtr("1000000"), DecoderVersion: "v1", SchemaVersion: "v1",
			}
			inserted, uErr := beRepo.UpsertTx(ctx, gTx, be)
			if uErr != nil {
				_ = gTx.Rollback()
				atomic.AddInt32(&errCount, 1)
				return
			}
			if inserted {
				atomic.AddInt32(&insertedCount, 1)
			}
			_ = gTx.Commit()
		}()
	}
	wg.Wait()

	// Verify: only 1 row should exist for this event_id.
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM balance_events WHERE event_id = $1", eventID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "exactly 1 row should exist for the event_id")
}

func TestBalanceEventRepo_FinalityUpgradePath(t *testing.T) {
	db := testDB(t)
	txRepo := postgres.NewTransactionRepo(db)
	tokenRepo := postgres.NewTokenRepo(db)
	beRepo := postgres.NewBalanceEventRepo(db)
	ctx := context.Background()
	txHash := "test-finality-tx-" + uuid.NewString()[:8]
	contract := "test-finality-token-" + uuid.NewString()[:8]
	eventID := "finality-evt-" + uuid.NewString()[:8]
	watchedAddr := "test-finality-" + uuid.NewString()[:8]

	setupTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	txID, err := txRepo.UpsertTx(ctx, setupTx, &model.Transaction{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		TxHash: txHash, FeeAmount: "5000", FeePayer: "payer1",
		Status: model.TxStatusSuccess, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)
	tokenID, err := tokenRepo.UpsertTx(ctx, setupTx, &model.Token{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		ContractAddress: contract, Symbol: "SOL", Name: "Solana",
		Decimals: 9, TokenType: model.TokenTypeNative, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)
	require.NoError(t, setupTx.Commit())

	baseBE := &model.BalanceEvent{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		TransactionID: txID, TxHash: txHash, TokenID: tokenID,
		EventCategory: "transfer", EventAction: "deposit",
		Address: watchedAddr, Delta: "1000000",
		WatchedAddress: &watchedAddr, BlockCursor: 100,
		ChainData: json.RawMessage("{}"), EventID: eventID,
		BalanceBefore: strPtr("0"), BalanceAfter: strPtr("1000000"),
		DecoderVersion: "v1", SchemaVersion: "v1",
	}

	// processed → confirmed → finalized
	finalityStates := []string{"processed", "confirmed", "finalized"}
	for _, fs := range finalityStates {
		tx, tErr := db.BeginTx(ctx, nil)
		require.NoError(t, tErr)
		baseBE.FinalityState = fs
		_, uErr := beRepo.UpsertTx(ctx, tx, baseBE)
		require.NoError(t, uErr)
		require.NoError(t, tx.Commit())
	}

	// Verify final state is finalized.
	var finalState string
	err = db.QueryRow("SELECT finality_state FROM balance_events WHERE event_id = $1", eventID).Scan(&finalState)
	require.NoError(t, err)
	assert.Equal(t, "finalized", finalState)
}

func TestBalanceEventRepo_RollbackDeleteByCursor(t *testing.T) {
	db := testDB(t)
	txRepo := postgres.NewTransactionRepo(db)
	tokenRepo := postgres.NewTokenRepo(db)
	beRepo := postgres.NewBalanceEventRepo(db)
	ctx := context.Background()

	watchedAddr := "test-rollback-" + uuid.NewString()[:8]
	contract := "test-rollback-token-" + uuid.NewString()[:8]

	setupTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	tokenID, err := tokenRepo.UpsertTx(ctx, setupTx, &model.Token{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		ContractAddress: contract, Symbol: "SOL", Name: "Solana",
		Decimals: 9, TokenType: model.TokenTypeNative, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)

	// Insert events at different cursors.
	for cursor := int64(10); cursor <= 15; cursor++ {
		txHash := fmt.Sprintf("rollback-tx-%d-%s", cursor, uuid.NewString()[:8])
		txID, tErr := txRepo.UpsertTx(ctx, setupTx, &model.Transaction{
			Chain: model.ChainSolana, Network: model.NetworkDevnet,
			TxHash: txHash, FeeAmount: "5000", FeePayer: "payer1",
			Status: model.TxStatusSuccess, ChainData: json.RawMessage("{}"),
			BlockCursor: cursor,
		})
		require.NoError(t, tErr)
		_, uErr := beRepo.UpsertTx(ctx, setupTx, &model.BalanceEvent{
			Chain: model.ChainSolana, Network: model.NetworkDevnet,
			TransactionID: txID, TxHash: txHash, TokenID: tokenID,
			EventCategory: "transfer", EventAction: "deposit",
			Address: watchedAddr, Delta: "100000",
			WatchedAddress: &watchedAddr, BlockCursor: cursor,
			ChainData: json.RawMessage("{}"),
			EventID:       fmt.Sprintf("rollback-evt-%d-%s", cursor, uuid.NewString()[:8]),
			FinalityState: "confirmed", BalanceBefore: strPtr("0"),
			BalanceAfter: strPtr("100000"), DecoderVersion: "v1", SchemaVersion: "v1",
		})
		require.NoError(t, uErr)
	}
	require.NoError(t, setupTx.Commit())

	// Delete events where block_cursor >= 13.
	deleteTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = deleteTx.ExecContext(ctx,
		"DELETE FROM balance_events WHERE chain = $1 AND network = $2 AND watched_address = $3 AND block_cursor >= $4",
		model.ChainSolana, model.NetworkDevnet, watchedAddr, 13,
	)
	require.NoError(t, err)
	require.NoError(t, deleteTx.Commit())

	// Count remaining events.
	var remaining int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM balance_events WHERE chain = $1 AND network = $2 AND watched_address = $3",
		model.ChainSolana, model.NetworkDevnet, watchedAddr,
	).Scan(&remaining)
	require.NoError(t, err)
	assert.Equal(t, 3, remaining, "events at cursor 10, 11, 12 should remain")
}

func TestBalanceRepo_ConcurrentAdjustBalance(t *testing.T) {
	db := testDB(t)
	tokenRepo := postgres.NewTokenRepo(db)
	balanceRepo := postgres.NewBalanceRepo(db)
	ctx := context.Background()
	addr := "test-concurrent-balance-" + uuid.NewString()[:8]
	contract := "test-concurrent-bal-token-" + uuid.NewString()[:8]

	setupTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	tokenID, err := tokenRepo.UpsertTx(ctx, setupTx, &model.Token{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		ContractAddress: contract, Symbol: "SOL", Name: "Solana",
		Decimals: 9, TokenType: model.TokenTypeNative, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)
	// Seed balance with 1_000_000_000.
	walletID := "wallet-1"
	orgID := "org-1"
	require.NoError(t, balanceRepo.AdjustBalanceTx(ctx, setupTx,
		model.ChainSolana, model.NetworkDevnet, addr,
		tokenID, &walletID, &orgID, "1000000000", 1, "tx-seed"))
	require.NoError(t, setupTx.Commit())

	// 10 goroutines each adjust by +100.
	const goroutines = 10
	const deltaPerGoroutine = "100"
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			gTx, gErr := db.BeginTx(ctx, nil)
			if gErr != nil {
				return
			}
			_ = balanceRepo.AdjustBalanceTx(ctx, gTx,
				model.ChainSolana, model.NetworkDevnet, addr,
				tokenID, &walletID, &orgID, deltaPerGoroutine, int64(10+idx), fmt.Sprintf("tx-concurrent-%d", idx))
			_ = gTx.Commit()
		}(i)
	}
	wg.Wait()

	// Final balance should be 1_000_000_000 + (10 * 100) = 1_000_001_000.
	readTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	amount, err := balanceRepo.GetAmountTx(ctx, readTx, model.ChainSolana, model.NetworkDevnet, addr, tokenID)
	require.NoError(t, err)
	require.NoError(t, readTx.Commit())
	assert.Equal(t, "1000001000", amount)
}

func TestIndexerConfigRepo_WatermarkOnlyAdvances(t *testing.T) {
	db := testDB(t)
	configRepo := postgres.NewIndexerConfigRepo(db)
	ctx := context.Background()

	// Use a unique chain/network to avoid conflicts with other tests.
	chain := model.ChainSolana
	network := model.NetworkDevnet

	// Set watermark to 100.
	tx1, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, configRepo.UpdateWatermarkTx(ctx, tx1, chain, network, 100))
	require.NoError(t, tx1.Commit())

	// Try to regress watermark to 50. GREATEST() should keep it at 100.
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, configRepo.UpdateWatermarkTx(ctx, tx2, chain, network, 50))
	require.NoError(t, tx2.Commit())

	// Verify watermark is still >= 100 (GREATEST semantics).
	var ingestedSeq int64
	err = db.QueryRow(
		"SELECT ingested_sequence FROM pipeline_watermarks WHERE chain = $1 AND network = $2",
		chain, network,
	).Scan(&ingestedSeq)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, ingestedSeq, int64(100), "watermark should not regress")

	// Advance watermark to 200.
	tx3, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, configRepo.UpdateWatermarkTx(ctx, tx3, chain, network, 200))
	require.NoError(t, tx3.Commit())

	err = db.QueryRow(
		"SELECT ingested_sequence FROM pipeline_watermarks WHERE chain = $1 AND network = $2",
		chain, network,
	).Scan(&ingestedSeq)
	require.NoError(t, err)
	assert.Equal(t, int64(200), ingestedSeq)
}
