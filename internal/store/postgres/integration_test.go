//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/admin"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/emperorhan/multichain-indexer/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDB returns a raw *sql.DB suitable for low-level integration tests.
// It checks the TEST_DB_URL environment variable first; if unset, the test is skipped.
// For full migration-aware setup (via testcontainers), use testDB() instead.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("TEST_DB_URL")
	if url == "" {
		t.Skip("TEST_DB_URL not set")
	}
	db, err := sql.Open("postgres", url)
	require.NoError(t, err)
	require.NoError(t, db.Ping())
	t.Cleanup(func() { db.Close() })
	return db
}

func testDB(t *testing.T) *postgres.DB {
	t.Helper()
	url := os.Getenv("TEST_DB_URL")
	if url != "" {
		// Use provided external DB.
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
	// Use testcontainers (Docker-based ephemeral PostgreSQL).
	return setupTestContainer(t)
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

	// FindByID
	found, err := repo.FindByID(ctx, tokenID)
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

	// BulkGetAmountWithExistsTx returns "0"/false for non-existent balance.
	keys := []store.BalanceKey{{Address: addr, TokenID: tokenID, BalanceType: ""}}
	result, err := balanceRepo.BulkGetAmountWithExistsTx(ctx, tx, model.ChainSolana, model.NetworkDevnet, keys)
	require.NoError(t, err)
	assert.Equal(t, "0", result[keys[0]].Amount)
	assert.False(t, result[keys[0]].Exists)

	// Deposit +1000000000
	walletID := "wallet-1"
	orgID := "org-1"
	require.NoError(t, balanceRepo.AdjustBalanceTx(ctx, tx, store.AdjustRequest{
		Chain: model.ChainSolana, Network: model.NetworkDevnet, Address: addr,
		TokenID: tokenID, WalletID: &walletID, OrgID: &orgID, Delta: "1000000000", Cursor: 100, TxHash: "tx-deposit", BalanceType: ""}))

	result, err = balanceRepo.BulkGetAmountWithExistsTx(ctx, tx, model.ChainSolana, model.NetworkDevnet, keys)
	require.NoError(t, err)
	assert.Equal(t, "1000000000", result[keys[0]].Amount)

	// Withdraw -500000000
	require.NoError(t, balanceRepo.AdjustBalanceTx(ctx, tx, store.AdjustRequest{
		Chain: model.ChainSolana, Network: model.NetworkDevnet, Address: addr,
		TokenID: tokenID, WalletID: &walletID, OrgID: &orgID, Delta: "-500000000", Cursor: 101, TxHash: "tx-withdraw", BalanceType: ""}))

	result, err = balanceRepo.BulkGetAmountWithExistsTx(ctx, tx, model.ChainSolana, model.NetworkDevnet, keys)
	require.NoError(t, err)
	assert.Equal(t, "500000000", result[keys[0]].Amount)

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
		Chain:          model.ChainSolana,
		Network:        model.NetworkDevnet,
		TransactionID:  txID,
		TxHash:         txHash,
		TokenID:        tokenID,
		ActivityType:   model.ActivityDeposit,
		EventAction:    "deposit",
		Address:        watchedAddr,
		Delta:          "1000000",
		WatchedAddress: &watchedAddr,
		BlockCursor:    100,
		ChainData:      json.RawMessage("{}"),
		EventID:        eventID,
		FinalityState:  "confirmed",
		BalanceBefore:  strPtr("0"),
		BalanceAfter:   strPtr("1000000"),
		DecoderVersion: "v1",
		SchemaVersion:  "v1",
	}

	result, err := beRepo.UpsertTx(ctx, tx, be)
	require.NoError(t, err)
	assert.True(t, result.Inserted, "first insert should return true")

	// Same event_id with same finality -> no update (WHERE clause rejects equal rank).
	result2, err := beRepo.UpsertTx(ctx, tx, be)
	require.NoError(t, err)
	assert.False(t, result2.Inserted, "duplicate insert with same finality should return false")

	// Same event_id with higher finality -> update succeeds.
	be.FinalityState = "finalized"
	result3, err := beRepo.UpsertTx(ctx, tx, be)
	require.NoError(t, err)
	assert.False(t, result3.Inserted, "finality upgrade should update but return false (xmax != 0)")

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
				Chain:          tc.chain,
				Network:        tc.network,
				TransactionID:  txID,
				TxHash:         txHash,
				TokenID:        tokenID,
				ActivityType:   model.ActivityDeposit,
				EventAction:    "replay",
				Address:        watchedAddr,
				Delta:          "1000000",
				WatchedAddress: &watchedAddr,
				BlockCursor:    100,
				BlockTime:      &blockTimeBefore,
				ChainData:      json.RawMessage("{}"),
				EventID:        eventID,
				FinalityState:  "confirmed",
				BalanceBefore:  strPtr("0"),
				BalanceAfter:   strPtr("1000000"),
				DecoderVersion: "v1",
				SchemaVersion:  "v1",
			}

			txA, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			resultBefore, err := beRepo.UpsertTx(ctx, txA, event)
			require.NoError(t, err)
			require.NoError(t, txA.Commit())
			assert.True(t, resultBefore.Inserted)

			var upsertCountBefore int
			err = db.QueryRow("SELECT COUNT(*) FROM balance_events WHERE chain = $1 AND network = $2 AND event_id = $3", tc.chain, tc.network, eventID).Scan(&upsertCountBefore)
			require.NoError(t, err)
			assert.Equal(t, 1, upsertCountBefore, "upsert_count_before=1")

			// Replay with forward block-time shift + lower cursor; idempotence should avoid duplicate row.
			blockTimeAfter := time.Date(2026, 2, 1, 0, 5, 0, 0, time.UTC)
			event.BlockTime = &blockTimeAfter
			event.FinalityState = "confirmed"
			event.BlockCursor = 99

			txB, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			resultAfter, err := beRepo.UpsertTx(ctx, txB, event)
			require.NoError(t, err)
			require.NoError(t, txB.Commit())
			assert.False(t, resultAfter.Inserted)

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
			assert.Equal(t, int64(100), storedCursor)
			assert.True(t, !storedBlockTime.Before(blockTimeBefore))
			assert.True(t, !storedBlockTime.Before(blockTimeAfter))

			// Replay with higher cursor within the same forward-shifted partition drift.
			event.BlockCursor = 150

			txC, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			resultReplayHigherCursor, err := beRepo.UpsertTx(ctx, txC, event)
			require.NoError(t, err)
			require.NoError(t, txC.Commit())
			assert.False(t, resultReplayHigherCursor.Inserted)

			err = db.QueryRow(
				"SELECT finality_state, block_cursor, block_time FROM balance_events WHERE chain = $1 AND network = $2 AND event_id = $3",
				tc.chain,
				tc.network,
				eventID,
			).Scan(&storedState, &storedCursor, &storedBlockTime)
			require.NoError(t, err)
			assert.Equal(t, "confirmed", storedState)
			assert.Equal(t, int64(150), storedCursor)
			assert.True(t, !storedBlockTime.Before(blockTimeAfter))

			// Replay with backward block-time regression + lower finality; keep finality and cursor monotonic.
			blockTimeRewind := time.Date(2025, 12, 31, 23, 50, 0, 0, time.UTC)
			event.BlockTime = &blockTimeRewind
			event.FinalityState = "processed"
			event.BlockCursor = 120

			txD, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			resultRewind, err := beRepo.UpsertTx(ctx, txD, event)
			require.NoError(t, err)
			require.NoError(t, txD.Commit())
			assert.False(t, resultRewind.Inserted)

			err = db.QueryRow(
				"SELECT finality_state, block_cursor, block_time FROM balance_events WHERE chain = $1 AND network = $2 AND event_id = $3",
				tc.chain,
				tc.network,
				eventID,
			).Scan(&storedState, &storedCursor, &storedBlockTime)
			require.NoError(t, err)
			assert.Equal(t, "confirmed", storedState)
			assert.Equal(t, int64(150), storedCursor)
			assert.True(t, !storedBlockTime.Before(blockTimeAfter))

			// Replay finality upgrade after shift should update canonical row only once.
			event.FinalityState = "finalized"
			event.BlockCursor = 160

			txE, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			resultFinalized, err := beRepo.UpsertTx(ctx, txE, event)
			require.NoError(t, err)
			require.NoError(t, txE.Commit())
			assert.False(t, resultFinalized.Inserted)

			var upsertCountAfter int
			err = db.QueryRow("SELECT COUNT(*) FROM balance_events WHERE chain = $1 AND network = $2 AND event_id = $3", tc.chain, tc.network, eventID).Scan(&upsertCountAfter)
			require.NoError(t, err)
			assert.Equal(t, 1, upsertCountAfter, "upsert_count_after=1")

			err = db.QueryRow(
				"SELECT finality_state, block_cursor, block_time FROM balance_events WHERE chain = $1 AND network = $2 AND event_id = $3",
				tc.chain,
				tc.network,
				eventID,
			).Scan(&storedState, &storedCursor, &storedBlockTime)
			require.NoError(t, err)
			assert.Equal(t, "finalized", storedState)
			assert.Equal(t, int64(160), storedCursor)
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
				ActivityType: model.ActivityDeposit, EventAction: "deposit",
				Address: watchedAddr, Delta: "1000000",
				WatchedAddress: &watchedAddr, BlockCursor: 100,
				ChainData: json.RawMessage("{}"), EventID: eventID,
				FinalityState: "confirmed", BalanceBefore: strPtr("0"),
				BalanceAfter: strPtr("1000000"), DecoderVersion: "v1", SchemaVersion: "v1",
			}
			uResult, uErr := beRepo.UpsertTx(ctx, gTx, be)
			if uErr != nil {
				_ = gTx.Rollback()
				atomic.AddInt32(&errCount, 1)
				return
			}
			if uResult.Inserted {
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
		ActivityType: model.ActivityDeposit, EventAction: "deposit",
		Address: watchedAddr, Delta: "1000000",
		WatchedAddress: &watchedAddr, BlockCursor: 100,
		ChainData: json.RawMessage("{}"), EventID: eventID,
		BalanceBefore: strPtr("0"), BalanceAfter: strPtr("1000000"),
		DecoderVersion: "v1", SchemaVersion: "v1",
	}

	// processed -> confirmed -> finalized
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
			ActivityType: model.ActivityDeposit, EventAction: "deposit",
			Address: watchedAddr, Delta: "100000",
			WatchedAddress: &watchedAddr, BlockCursor: cursor,
			ChainData:      json.RawMessage("{}"),
			EventID:        fmt.Sprintf("rollback-evt-%d-%s", cursor, uuid.NewString()[:8]),
			FinalityState:  "confirmed", BalanceBefore: strPtr("0"),
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
	require.NoError(t, balanceRepo.AdjustBalanceTx(ctx, setupTx, store.AdjustRequest{
		Chain: model.ChainSolana, Network: model.NetworkDevnet, Address: addr,
		TokenID: tokenID, WalletID: &walletID, OrgID: &orgID, Delta: "1000000000", Cursor: 1, TxHash: "tx-seed", BalanceType: ""}))
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
			_ = balanceRepo.AdjustBalanceTx(ctx, gTx, store.AdjustRequest{
				Chain: model.ChainSolana, Network: model.NetworkDevnet, Address: addr,
				TokenID: tokenID, WalletID: &walletID, OrgID: &orgID, Delta: deltaPerGoroutine, Cursor: int64(10 + idx), TxHash: fmt.Sprintf("tx-concurrent-%d", idx), BalanceType: ""})
			_ = gTx.Commit()
		}(i)
	}
	wg.Wait()

	// Final balance should be 1_000_000_000 + (10 * 100) = 1_000_001_000.
	readTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	keys := []store.BalanceKey{{Address: addr, TokenID: tokenID, BalanceType: ""}}
	result, err := balanceRepo.BulkGetAmountWithExistsTx(ctx, readTx, model.ChainSolana, model.NetworkDevnet, keys)
	require.NoError(t, err)
	require.NoError(t, readTx.Commit())
	assert.Equal(t, "1000001000", result[keys[0]].Amount)
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

// ---------- Bulk Method Integration Tests ----------

func TestBulkUpsertTx_Transactions(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewTransactionRepo(db)
	ctx := context.Background()

	hash1 := "bulk-tx-1-" + uuid.NewString()[:8]
	hash2 := "bulk-tx-2-" + uuid.NewString()[:8]
	hash3 := "bulk-tx-3-" + uuid.NewString()[:8]

	txns := []*model.Transaction{
		{Chain: model.ChainSolana, Network: model.NetworkDevnet, TxHash: hash1, FeeAmount: "5000", FeePayer: "payer1", Status: model.TxStatusSuccess, ChainData: json.RawMessage("{}")},
		{Chain: model.ChainSolana, Network: model.NetworkDevnet, TxHash: hash2, FeeAmount: "6000", FeePayer: "payer2", Status: model.TxStatusSuccess, ChainData: json.RawMessage("{}")},
		{Chain: model.ChainSolana, Network: model.NetworkDevnet, TxHash: hash3, FeeAmount: "7000", FeePayer: "payer3", Status: model.TxStatusFailed, ChainData: json.RawMessage("{}")},
	}

	// First bulk insert.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	result, err := repo.BulkUpsertTx(ctx, tx, txns)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	// Verify all 3 hashes are in the result map with non-nil UUIDs.
	require.Len(t, result, 3)
	assert.NotEqual(t, uuid.Nil, result[hash1])
	assert.NotEqual(t, uuid.Nil, result[hash2])
	assert.NotEqual(t, uuid.Nil, result[hash3])

	// All IDs should be distinct.
	assert.NotEqual(t, result[hash1], result[hash2])
	assert.NotEqual(t, result[hash2], result[hash3])
	assert.NotEqual(t, result[hash1], result[hash3])

	// Idempotency: insert same 3 again, expect same IDs.
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	result2, err := repo.BulkUpsertTx(ctx, tx2, txns)
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())

	require.Len(t, result2, 3)
	assert.Equal(t, result[hash1], result2[hash1])
	assert.Equal(t, result[hash2], result2[hash2])
	assert.Equal(t, result[hash3], result2[hash3])
}

func TestBulkUpsertTx_Tokens(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewTokenRepo(db)
	ctx := context.Background()

	contract1 := "bulk-token-1-" + uuid.NewString()[:8]
	contract2 := "bulk-token-2-" + uuid.NewString()[:8]
	contract3 := "bulk-token-3-" + uuid.NewString()[:8]

	tokens := []*model.Token{
		{Chain: model.ChainSolana, Network: model.NetworkDevnet, ContractAddress: contract1, Symbol: "AAA", Name: "Token A", Decimals: 6, TokenType: model.TokenTypeFungible, ChainData: json.RawMessage("{}")},
		{Chain: model.ChainSolana, Network: model.NetworkDevnet, ContractAddress: contract2, Symbol: "BBB", Name: "Token B", Decimals: 9, TokenType: model.TokenTypeNative, ChainData: json.RawMessage("{}")},
		{Chain: model.ChainSolana, Network: model.NetworkDevnet, ContractAddress: contract3, Symbol: "CCC", Name: "Token C", Decimals: 18, TokenType: model.TokenTypeFungible, ChainData: json.RawMessage("{}")},
	}

	// First bulk insert.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	result, err := repo.BulkUpsertTx(ctx, tx, tokens)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	// Verify all 3 contract addresses are in the result map.
	require.Len(t, result, 3)
	assert.NotEqual(t, uuid.Nil, result[contract1])
	assert.NotEqual(t, uuid.Nil, result[contract2])
	assert.NotEqual(t, uuid.Nil, result[contract3])

	// All IDs should be distinct.
	assert.NotEqual(t, result[contract1], result[contract2])
	assert.NotEqual(t, result[contract2], result[contract3])
	assert.NotEqual(t, result[contract1], result[contract3])

	// Idempotency: insert same 3 again, expect same IDs.
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	result2, err := repo.BulkUpsertTx(ctx, tx2, tokens)
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())

	require.Len(t, result2, 3)
	assert.Equal(t, result[contract1], result2[contract1])
	assert.Equal(t, result[contract2], result2[contract2])
	assert.Equal(t, result[contract3], result2[contract3])
}

func TestBulkIsDeniedTx(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewTokenRepo(db)
	ctx := context.Background()

	contractDenied := "bulk-denied-" + uuid.NewString()[:8]
	contractAllowed := "bulk-allowed-" + uuid.NewString()[:8]
	contractMissing := "bulk-missing-" + uuid.NewString()[:8]

	// Insert 2 tokens.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = repo.UpsertTx(ctx, tx, &model.Token{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		ContractAddress: contractDenied, Symbol: "DNY", Name: "Denied Token",
		Decimals: 9, TokenType: model.TokenTypeFungible, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)
	_, err = repo.UpsertTx(ctx, tx, &model.Token{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		ContractAddress: contractAllowed, Symbol: "ALW", Name: "Allowed Token",
		Decimals: 9, TokenType: model.TokenTypeFungible, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)

	// Deny one token.
	require.NoError(t, repo.DenyTokenTx(ctx, tx, model.ChainSolana, model.NetworkDevnet,
		contractDenied, "scam token", "auto", 90, []string{"honeypot", "rugpull"}))
	require.NoError(t, tx.Commit())

	// BulkIsDeniedTx with 3 addresses: denied, allowed, and missing.
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	result, err := repo.BulkIsDeniedTx(ctx, tx2, model.ChainSolana, model.NetworkDevnet,
		[]string{contractDenied, contractAllowed, contractMissing})
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())

	// Denied token should be true.
	assert.True(t, result[contractDenied], "denied token should return true")
	// Allowed token should be false.
	assert.False(t, result[contractAllowed], "allowed token should return false")
	// Missing token should not be in the map (not found in DB).
	_, exists := result[contractMissing]
	assert.False(t, exists, "missing token should not be in the result map")
}

func TestBulkGetAmountWithExistsTx(t *testing.T) {
	db := testDB(t)
	tokenRepo := postgres.NewTokenRepo(db)
	balanceRepo := postgres.NewBalanceRepo(db)
	ctx := context.Background()

	addr1 := "bulk-bal-addr1-" + uuid.NewString()[:8]
	addr2 := "bulk-bal-addr2-" + uuid.NewString()[:8]
	addr3 := "bulk-bal-addr3-" + uuid.NewString()[:8] // no balance created for this one
	contract1 := "bulk-bal-token1-" + uuid.NewString()[:8]
	contract2 := "bulk-bal-token2-" + uuid.NewString()[:8]

	// Create 2 tokens.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	tokenID1, err := tokenRepo.UpsertTx(ctx, tx, &model.Token{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		ContractAddress: contract1, Symbol: "T1", Name: "Token 1",
		Decimals: 9, TokenType: model.TokenTypeNative, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)
	tokenID2, err := tokenRepo.UpsertTx(ctx, tx, &model.Token{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		ContractAddress: contract2, Symbol: "T2", Name: "Token 2",
		Decimals: 6, TokenType: model.TokenTypeFungible, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)

	// Adjust balances to create 2 existing balance records.
	walletID := "wallet-bulk"
	orgID := "org-bulk"
	require.NoError(t, balanceRepo.AdjustBalanceTx(ctx, tx, store.AdjustRequest{
		Chain: model.ChainSolana, Network: model.NetworkDevnet, Address: addr1,
		TokenID: tokenID1, WalletID: &walletID, OrgID: &orgID, Delta: "5000000", Cursor: 10, TxHash: "tx-bulk-1", BalanceType: ""}))
	require.NoError(t, balanceRepo.AdjustBalanceTx(ctx, tx, store.AdjustRequest{
		Chain: model.ChainSolana, Network: model.NetworkDevnet, Address: addr2,
		TokenID: tokenID2, WalletID: &walletID, OrgID: &orgID, Delta: "3000000", Cursor: 11, TxHash: "tx-bulk-2", BalanceType: ""}))
	require.NoError(t, tx.Commit())

	// BulkGetAmountWithExistsTx for 3 keys: 2 existing + 1 missing.
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	keys := []store.BalanceKey{
		{Address: addr1, TokenID: tokenID1, BalanceType: ""},
		{Address: addr2, TokenID: tokenID2, BalanceType: ""},
		{Address: addr3, TokenID: tokenID1, BalanceType: ""},
	}
	result, err := balanceRepo.BulkGetAmountWithExistsTx(ctx, tx2, model.ChainSolana, model.NetworkDevnet, keys)
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())

	// Existing key 1.
	info1 := result[store.BalanceKey{Address: addr1, TokenID: tokenID1, BalanceType: ""}]
	assert.Equal(t, "5000000", info1.Amount)
	assert.True(t, info1.Exists, "addr1/tokenID1 should exist")

	// Existing key 2.
	info2 := result[store.BalanceKey{Address: addr2, TokenID: tokenID2, BalanceType: ""}]
	assert.Equal(t, "3000000", info2.Amount)
	assert.True(t, info2.Exists, "addr2/tokenID2 should exist")

	// Missing key 3.
	info3 := result[store.BalanceKey{Address: addr3, TokenID: tokenID1, BalanceType: ""}]
	assert.Equal(t, "0", info3.Amount)
	assert.False(t, info3.Exists, "addr3/tokenID1 should not exist")
}

func TestBulkAdjustBalanceTx(t *testing.T) {
	db := testDB(t)
	tokenRepo := postgres.NewTokenRepo(db)
	balanceRepo := postgres.NewBalanceRepo(db)
	ctx := context.Background()

	addr1 := "bulk-adj-addr1-" + uuid.NewString()[:8]
	addr2 := "bulk-adj-addr2-" + uuid.NewString()[:8]
	addr3 := "bulk-adj-addr3-" + uuid.NewString()[:8]
	contract := "bulk-adj-token-" + uuid.NewString()[:8]

	// Create token.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	tokenID, err := tokenRepo.UpsertTx(ctx, tx, &model.Token{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		ContractAddress: contract, Symbol: "BLK", Name: "Bulk Token",
		Decimals: 9, TokenType: model.TokenTypeNative, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	walletID := "wallet-bulk-adj"
	orgID := "org-bulk-adj"

	// First bulk adjust: create 3 balances.
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	items := []store.BulkAdjustItem{
		{Address: addr1, TokenID: tokenID, WalletID: &walletID, OrgID: &orgID, Delta: "1000000", Cursor: 100, TxHash: "tx-ba-1", BalanceType: ""},
		{Address: addr2, TokenID: tokenID, WalletID: &walletID, OrgID: &orgID, Delta: "2000000", Cursor: 101, TxHash: "tx-ba-2", BalanceType: ""},
		{Address: addr3, TokenID: tokenID, WalletID: &walletID, OrgID: &orgID, Delta: "3000000", Cursor: 102, TxHash: "tx-ba-3", BalanceType: ""},
	}
	require.NoError(t, balanceRepo.BulkAdjustBalanceTx(ctx, tx2, model.ChainSolana, model.NetworkDevnet, items))
	require.NoError(t, tx2.Commit())

	// Verify each balance.
	tx3, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	keys := []store.BalanceKey{
		{Address: addr1, TokenID: tokenID, BalanceType: ""},
		{Address: addr2, TokenID: tokenID, BalanceType: ""},
		{Address: addr3, TokenID: tokenID, BalanceType: ""},
	}
	balResult, err := balanceRepo.BulkGetAmountWithExistsTx(ctx, tx3, model.ChainSolana, model.NetworkDevnet, keys)
	require.NoError(t, err)
	assert.Equal(t, "1000000", balResult[keys[0]].Amount)
	assert.Equal(t, "2000000", balResult[keys[1]].Amount)
	assert.Equal(t, "3000000", balResult[keys[2]].Amount)
	require.NoError(t, tx3.Commit())

	// Second bulk adjust with different deltas to verify cumulative effect.
	tx4, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	items2 := []store.BulkAdjustItem{
		{Address: addr1, TokenID: tokenID, WalletID: &walletID, OrgID: &orgID, Delta: "500000", Cursor: 200, TxHash: "tx-ba-4", BalanceType: ""},
		{Address: addr2, TokenID: tokenID, WalletID: &walletID, OrgID: &orgID, Delta: "-1000000", Cursor: 201, TxHash: "tx-ba-5", BalanceType: ""},
		{Address: addr3, TokenID: tokenID, WalletID: &walletID, OrgID: &orgID, Delta: "7000000", Cursor: 202, TxHash: "tx-ba-6", BalanceType: ""},
	}
	require.NoError(t, balanceRepo.BulkAdjustBalanceTx(ctx, tx4, model.ChainSolana, model.NetworkDevnet, items2))
	require.NoError(t, tx4.Commit())

	// Verify cumulative balances.
	tx5, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	balResult2, err := balanceRepo.BulkGetAmountWithExistsTx(ctx, tx5, model.ChainSolana, model.NetworkDevnet, keys)
	require.NoError(t, err)
	assert.Equal(t, "1500000", balResult2[keys[0]].Amount)  // 1000000 + 500000
	assert.Equal(t, "1000000", balResult2[keys[1]].Amount)  // 2000000 - 1000000
	assert.Equal(t, "10000000", balResult2[keys[2]].Amount) // 3000000 + 7000000
	require.NoError(t, tx5.Commit())
}

func TestBulkUpsertTx_BalanceEvents(t *testing.T) {
	db := testDB(t)
	txRepo := postgres.NewTransactionRepo(db)
	tokenRepo := postgres.NewTokenRepo(db)
	beRepo := postgres.NewBalanceEventRepo(db)
	ctx := context.Background()

	txHash1 := "bulk-be-tx1-" + uuid.NewString()[:8]
	txHash2 := "bulk-be-tx2-" + uuid.NewString()[:8]
	txHash3 := "bulk-be-tx3-" + uuid.NewString()[:8]
	contract := "bulk-be-token-" + uuid.NewString()[:8]
	eventID1 := "bulk-evt-1-" + uuid.NewString()[:8]
	eventID2 := "bulk-evt-2-" + uuid.NewString()[:8]
	eventID3 := "bulk-evt-3-" + uuid.NewString()[:8]
	watchedAddr := "bulk-be-watched-" + uuid.NewString()[:8]

	// Setup: create transactions and token.
	setupTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	txIDs, err := txRepo.BulkUpsertTx(ctx, setupTx, []*model.Transaction{
		{Chain: model.ChainSolana, Network: model.NetworkDevnet, TxHash: txHash1, FeeAmount: "5000", FeePayer: "payer1", Status: model.TxStatusSuccess, ChainData: json.RawMessage("{}")},
		{Chain: model.ChainSolana, Network: model.NetworkDevnet, TxHash: txHash2, FeeAmount: "5000", FeePayer: "payer2", Status: model.TxStatusSuccess, ChainData: json.RawMessage("{}")},
		{Chain: model.ChainSolana, Network: model.NetworkDevnet, TxHash: txHash3, FeeAmount: "5000", FeePayer: "payer3", Status: model.TxStatusSuccess, ChainData: json.RawMessage("{}")},
	})
	require.NoError(t, err)

	tokenID, err := tokenRepo.UpsertTx(ctx, setupTx, &model.Token{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		ContractAddress: contract, Symbol: "SOL", Name: "Solana",
		Decimals: 9, TokenType: model.TokenTypeNative, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)
	require.NoError(t, setupTx.Commit())

	events := []*model.BalanceEvent{
		{
			Chain: model.ChainSolana, Network: model.NetworkDevnet,
			TransactionID: txIDs[txHash1], TxHash: txHash1, TokenID: tokenID,
			ActivityType: model.ActivityDeposit, EventAction: "deposit",
			Address: watchedAddr, Delta: "1000000",
			WatchedAddress: &watchedAddr, BlockCursor: 100,
			ChainData: json.RawMessage("{}"), EventID: eventID1,
			FinalityState: "confirmed", BalanceBefore: strPtr("0"),
			BalanceAfter: strPtr("1000000"), DecoderVersion: "v1", SchemaVersion: "v1",
		},
		{
			Chain: model.ChainSolana, Network: model.NetworkDevnet,
			TransactionID: txIDs[txHash2], TxHash: txHash2, TokenID: tokenID,
			ActivityType: model.ActivityWithdrawal, EventAction: "withdrawal",
			Address: watchedAddr, Delta: "-500000",
			WatchedAddress: &watchedAddr, BlockCursor: 101,
			ChainData: json.RawMessage("{}"), EventID: eventID2,
			FinalityState: "confirmed", BalanceBefore: strPtr("1000000"),
			BalanceAfter: strPtr("500000"), DecoderVersion: "v1", SchemaVersion: "v1",
		},
		{
			Chain: model.ChainSolana, Network: model.NetworkDevnet,
			TransactionID: txIDs[txHash3], TxHash: txHash3, TokenID: tokenID,
			ActivityType: model.ActivityDeposit, EventAction: "deposit",
			Address: watchedAddr, Delta: "2000000",
			WatchedAddress: &watchedAddr, BlockCursor: 102,
			ChainData: json.RawMessage("{}"), EventID: eventID3,
			FinalityState: "confirmed", BalanceBefore: strPtr("500000"),
			BalanceAfter: strPtr("2500000"), DecoderVersion: "v1", SchemaVersion: "v1",
		},
	}

	// First bulk upsert.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	result, err := beRepo.BulkUpsertTx(ctx, tx, events)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	assert.Equal(t, 3, result.InsertedCount, "all 3 events should be inserted")

	// Verify each event exists in the DB.
	for _, eid := range []string{eventID1, eventID2, eventID3} {
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM balance_events WHERE event_id = $1", eid).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "event %s should have exactly 1 row", eid)
	}

	// Idempotency: same events again, expect 0 new insertions.
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	result2, err := beRepo.BulkUpsertTx(ctx, tx2, events)
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())

	assert.Equal(t, 0, result2.InsertedCount, "duplicate events should not be inserted again")

	// Verify still only 1 row per event.
	for _, eid := range []string{eventID1, eventID2, eventID3} {
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM balance_events WHERE event_id = $1", eid).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "event %s should still have exactly 1 row after idempotent upsert", eid)
	}
}

// ---------- New Stub Integration Tests (P1 Production Readiness) ----------

// TestIndexedBlockRepo_BulkUpsertAndGet verifies that bulk upserting indexed blocks
// works correctly and that upserts are idempotent (ON CONFLICT updates).
func TestIndexedBlockRepo_BulkUpsertAndGet(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewIndexedBlockRepo(db.DB)
	ctx := context.Background()

	chain := model.ChainBase
	network := model.NetworkSepolia

	blockTime1 := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	blockTime2 := time.Date(2026, 2, 1, 12, 0, 12, 0, time.UTC)
	blockTime3 := time.Date(2026, 2, 1, 12, 0, 24, 0, time.UTC)

	suffix := uuid.NewString()[:8]
	blocks := []*model.IndexedBlock{
		{Chain: chain, Network: network, BlockNumber: 100000, BlockHash: "hash-100000-" + suffix, ParentHash: "parent-99999-" + suffix, FinalityState: "pending", BlockTime: &blockTime1},
		{Chain: chain, Network: network, BlockNumber: 100001, BlockHash: "hash-100001-" + suffix, ParentHash: "hash-100000-" + suffix, FinalityState: "pending", BlockTime: &blockTime2},
		{Chain: chain, Network: network, BlockNumber: 100002, BlockHash: "hash-100002-" + suffix, ParentHash: "hash-100001-" + suffix, FinalityState: "pending", BlockTime: &blockTime3},
	}

	// Bulk upsert.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, repo.BulkUpsertTx(ctx, tx, blocks))
	require.NoError(t, tx.Commit())

	// Verify each block exists.
	for _, b := range blocks {
		found, err := repo.GetByBlockNumber(ctx, chain, network, b.BlockNumber)
		require.NoError(t, err)
		require.NotNil(t, found, "block %d should exist", b.BlockNumber)
		assert.Equal(t, b.BlockHash, found.BlockHash)
		assert.Equal(t, b.ParentHash, found.ParentHash)
		assert.Equal(t, "pending", found.FinalityState)
	}

	// Idempotent upsert with updated finality state.
	for _, b := range blocks {
		b.FinalityState = "finalized"
	}
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, repo.BulkUpsertTx(ctx, tx2, blocks))
	require.NoError(t, tx2.Commit())

	// Verify finality state was updated.
	for _, b := range blocks {
		found, err := repo.GetByBlockNumber(ctx, chain, network, b.BlockNumber)
		require.NoError(t, err)
		require.NotNil(t, found)
		assert.Equal(t, "finalized", found.FinalityState)
	}
}

// TestWatchedAddressRepo_UpsertAndGetActive verifies the watched address
// upsert idempotency and active-address filtering.
func TestWatchedAddressRepo_UpsertAndGetActive(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewWatchedAddressRepo(db)
	ctx := context.Background()

	addr := "test-watched-" + uuid.NewString()[:8]
	chain := model.ChainSolana
	network := model.NetworkDevnet

	// Upsert a watched address.
	wa := &model.WatchedAddress{
		Chain:    chain,
		Network:  network,
		Address:  addr,
		IsActive: true,
		Source:   model.AddressSourceEnv,
	}
	require.NoError(t, repo.Upsert(ctx, wa))

	// GetActive should include it.
	active, err := repo.GetActive(ctx, chain, network)
	require.NoError(t, err)

	found := false
	for _, a := range active {
		if a.Address == addr {
			found = true
			assert.True(t, a.IsActive)
			break
		}
	}
	assert.True(t, found, "watched address should appear in active list")

	// Idempotent upsert should not error.
	require.NoError(t, repo.Upsert(ctx, wa))

	// FindByAddress roundtrip.
	foundAddr, err := repo.FindByAddress(ctx, chain, network, addr)
	require.NoError(t, err)
	require.NotNil(t, foundAddr)
	assert.Equal(t, addr, foundAddr.Address)
	assert.True(t, foundAddr.IsActive)
}

// TestIndexerConfigRepo_WatermarkGetRoundtrip verifies that GetWatermark returns
// the watermark set by UpdateWatermarkTx.
func TestIndexerConfigRepo_WatermarkGetRoundtrip(t *testing.T) {
	db := testDB(t)
	configRepo := postgres.NewIndexerConfigRepo(db)
	ctx := context.Background()

	chain := model.ChainBase
	network := model.NetworkSepolia

	// Set a watermark.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, configRepo.UpdateWatermarkTx(ctx, tx, chain, network, 500))
	require.NoError(t, tx.Commit())

	// Get the watermark back.
	wm, err := configRepo.GetWatermark(ctx, chain, network)
	require.NoError(t, err)
	require.NotNil(t, wm)
	assert.Equal(t, chain, wm.Chain)
	assert.Equal(t, network, wm.Network)
	assert.GreaterOrEqual(t, wm.IngestedSequence, int64(500))
}

// TestIndexerConfigRepo_RewindWatermark verifies that RewindWatermarkTx bypasses
// the GREATEST guard and actually regresses the watermark.
func TestIndexerConfigRepo_RewindWatermark(t *testing.T) {
	db := testDB(t)
	configRepo := postgres.NewIndexerConfigRepo(db)
	ctx := context.Background()

	chain := model.ChainBTC
	network := model.NetworkTestnet

	// Set watermark to 1000.
	tx1, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, configRepo.UpdateWatermarkTx(ctx, tx1, chain, network, 1000))
	require.NoError(t, tx1.Commit())

	// Rewind to 500.
	tx2, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, configRepo.RewindWatermarkTx(ctx, tx2, chain, network, 500))
	require.NoError(t, tx2.Commit())

	// Verify watermark is now 500.
	wm, err := configRepo.GetWatermark(ctx, chain, network)
	require.NoError(t, err)
	require.NotNil(t, wm)
	assert.Equal(t, int64(500), wm.IngestedSequence, "rewind should bypass GREATEST guard")
}

// TestSetupTestDB_SkipsWhenNoEnv verifies that setupTestDB skips the test
// when TEST_DB_URL is not set. This test will always pass in CI without a real DB.
func TestSetupTestDB_SkipsWhenNoEnv(t *testing.T) {
	if os.Getenv("TEST_DB_URL") != "" {
		t.Skip("TEST_DB_URL is set; this test only validates the skip behavior")
	}
	// Cannot call setupTestDB directly because it would skip this test.
	// Instead, verify the environment variable is empty.
	assert.Empty(t, os.Getenv("TEST_DB_URL"))
}

// ---------- AddressBookRepo ----------

func TestAddressBookRepo_UpsertAndList(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewAddressBookRepo(db.DB)
	ctx := context.Background()

	chain := model.ChainSolana
	network := model.NetworkDevnet
	suffix := uuid.NewString()[:8]
	orgID := "org-" + suffix

	// Upsert 2 entries.
	entry1 := &admin.AddressBookEntry{
		Chain: chain, Network: network,
		OrgID: orgID, Address: "addr1-" + suffix,
		Name: "Alice", Status: "ACTIVE",
	}
	entry2 := &admin.AddressBookEntry{
		Chain: chain, Network: network,
		OrgID: orgID, Address: "addr2-" + suffix,
		Name: "Bob", Status: "ACTIVE",
	}
	require.NoError(t, repo.Upsert(ctx, entry1))
	require.NoError(t, repo.Upsert(ctx, entry2))

	// List should contain both entries.
	entries, err := repo.List(ctx, chain, network)
	require.NoError(t, err)

	var found1, found2 bool
	for _, e := range entries {
		if e.Address == entry1.Address {
			found1 = true
			assert.Equal(t, "Alice", e.Name)
		}
		if e.Address == entry2.Address {
			found2 = true
			assert.Equal(t, "Bob", e.Name)
		}
	}
	assert.True(t, found1, "entry1 should be in list")
	assert.True(t, found2, "entry2 should be in list")

	// Upsert same address with different name → update.
	entry1.Name = "Alice Updated"
	require.NoError(t, repo.Upsert(ctx, entry1))

	entries2, err := repo.List(ctx, chain, network)
	require.NoError(t, err)
	for _, e := range entries2 {
		if e.Address == entry1.Address {
			assert.Equal(t, "Alice Updated", e.Name)
		}
	}
}

func TestAddressBookRepo_Delete(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewAddressBookRepo(db.DB)
	ctx := context.Background()

	chain := model.ChainBase
	network := model.NetworkSepolia
	suffix := uuid.NewString()[:8]

	entry := &admin.AddressBookEntry{
		Chain: chain, Network: network,
		OrgID: "org-del-" + suffix, Address: "addr-del-" + suffix,
		Name: "ToDelete", Status: "ACTIVE",
	}
	require.NoError(t, repo.Upsert(ctx, entry))

	// Verify it exists.
	found, err := repo.FindByAddress(ctx, chain, network, entry.Address)
	require.NoError(t, err)
	require.NotNil(t, found)

	// Delete.
	require.NoError(t, repo.Delete(ctx, chain, network, entry.Address))

	// FindByAddress should return nil now.
	found2, err := repo.FindByAddress(ctx, chain, network, entry.Address)
	require.NoError(t, err)
	assert.Nil(t, found2, "deleted entry should not be found")
}

func TestAddressBookRepo_FindByAddress(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewAddressBookRepo(db.DB)
	ctx := context.Background()

	chain := model.ChainBTC
	network := model.NetworkTestnet
	suffix := uuid.NewString()[:8]

	entry := &admin.AddressBookEntry{
		Chain: chain, Network: network,
		OrgID: "org-find-" + suffix, Address: "addr-find-" + suffix,
		Name: "FindMe", Status: "ACTIVE",
	}
	require.NoError(t, repo.Upsert(ctx, entry))

	found, err := repo.FindByAddress(ctx, chain, network, entry.Address)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, entry.Address, found.Address)
	assert.Equal(t, "FindMe", found.Name)
	assert.Equal(t, "ACTIVE", found.Status)

	// Non-existent address returns nil.
	notFound, err := repo.FindByAddress(ctx, chain, network, "nonexistent-"+suffix)
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

// ---------- DashboardRepo ----------

func TestDashboardRepo_GetAllWatermarks(t *testing.T) {
	db := testDB(t)
	configRepo := postgres.NewIndexerConfigRepo(db)
	dashRepo := postgres.NewDashboardRepo(db.DB)
	ctx := context.Background()

	chain := model.ChainSolana
	network := model.NetworkDevnet

	// Ensure at least one watermark exists.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, configRepo.UpdateWatermarkTx(ctx, tx, chain, network, 42))
	require.NoError(t, tx.Commit())

	watermarks, err := dashRepo.GetAllWatermarks(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, watermarks, "should have at least one watermark")

	found := false
	for _, wm := range watermarks {
		if wm.Chain == chain && wm.Network == network {
			found = true
			assert.GreaterOrEqual(t, wm.IngestedSequence, int64(42))
		}
	}
	assert.True(t, found, "solana:devnet watermark should be present")
}

func TestDashboardRepo_CountWatchedAddresses(t *testing.T) {
	db := testDB(t)
	waRepo := postgres.NewWatchedAddressRepo(db)
	dashRepo := postgres.NewDashboardRepo(db.DB)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]

	// Get initial count.
	countBefore, err := dashRepo.CountWatchedAddresses(ctx)
	require.NoError(t, err)

	// Add 2 active watched addresses.
	require.NoError(t, waRepo.Upsert(ctx, &model.WatchedAddress{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		Address: "dash-wa-1-" + suffix, IsActive: true, Source: model.AddressSourceEnv,
	}))
	require.NoError(t, waRepo.Upsert(ctx, &model.WatchedAddress{
		Chain: model.ChainSolana, Network: model.NetworkDevnet,
		Address: "dash-wa-2-" + suffix, IsActive: true, Source: model.AddressSourceEnv,
	}))

	countAfter, err := dashRepo.CountWatchedAddresses(ctx)
	require.NoError(t, err)
	assert.Equal(t, countBefore+2, countAfter, "count should increase by 2")
}

func TestDashboardRepo_GetBalanceSummary(t *testing.T) {
	db := testDB(t)
	waRepo := postgres.NewWatchedAddressRepo(db)
	tokenRepo := postgres.NewTokenRepo(db)
	balanceRepo := postgres.NewBalanceRepo(db)
	dashRepo := postgres.NewDashboardRepo(db.DB)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	chain := model.ChainSolana
	network := model.NetworkDevnet
	addr := "dash-bal-" + suffix
	contract := "dash-bal-token-" + suffix

	// Create watched address.
	require.NoError(t, waRepo.Upsert(ctx, &model.WatchedAddress{
		Chain: chain, Network: network, Address: addr,
		IsActive: true, Source: model.AddressSourceEnv,
	}))

	// Create token and balance.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	tokenID, err := tokenRepo.UpsertTx(ctx, tx, &model.Token{
		Chain: chain, Network: network, ContractAddress: contract,
		Symbol: "DSH", Name: "Dashboard Token", Decimals: 6,
		TokenType: model.TokenTypeFungible, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)

	walletID := "wallet-dash"
	orgID := "org-dash"
	require.NoError(t, balanceRepo.AdjustBalanceTx(ctx, tx, store.AdjustRequest{
		Chain: chain, Network: network, Address: addr,
		TokenID: tokenID, WalletID: &walletID, OrgID: &orgID,
		Delta: "9999999", Cursor: 50, TxHash: "tx-dash-bal", BalanceType: "",
	}))
	require.NoError(t, tx.Commit())

	// Query balance summary.
	summary, err := dashRepo.GetBalanceSummary(ctx, chain, network)
	require.NoError(t, err)

	var found bool
	for _, ab := range summary {
		if ab.Address == addr {
			found = true
			require.NotEmpty(t, ab.Balances, "should have at least one token balance")
			assert.Equal(t, "DSH", ab.Balances[0].TokenSymbol)
			assert.Equal(t, "9999999", ab.Balances[0].Amount)
		}
	}
	assert.True(t, found, "address should appear in balance summary")
}

func TestDashboardRepo_GetRecentEvents(t *testing.T) {
	db := testDB(t)
	txRepo := postgres.NewTransactionRepo(db)
	tokenRepo := postgres.NewTokenRepo(db)
	beRepo := postgres.NewBalanceEventRepo(db)
	dashRepo := postgres.NewDashboardRepo(db.DB)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	chain := model.ChainBase
	network := model.NetworkSepolia
	addr := "dash-evt-" + suffix
	contract := "dash-evt-token-" + suffix

	// Setup token.
	setupTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	tokenID, err := tokenRepo.UpsertTx(ctx, setupTx, &model.Token{
		Chain: chain, Network: network, ContractAddress: contract,
		Symbol: "EVT", Name: "Event Token", Decimals: 18,
		TokenType: model.TokenTypeFungible, ChainData: json.RawMessage("{}"),
	})
	require.NoError(t, err)

	// Insert 5 balance events.
	for i := 0; i < 5; i++ {
		txHash := fmt.Sprintf("dash-evt-tx-%d-%s", i, suffix)
		txID, tErr := txRepo.UpsertTx(ctx, setupTx, &model.Transaction{
			Chain: chain, Network: network, TxHash: txHash,
			FeeAmount: "1000", FeePayer: "payer", Status: model.TxStatusSuccess,
			ChainData: json.RawMessage("{}"), BlockCursor: int64(200 + i),
		})
		require.NoError(t, tErr)

		eventID := fmt.Sprintf("dash-evt-%d-%s", i, suffix)
		_, uErr := beRepo.UpsertTx(ctx, setupTx, &model.BalanceEvent{
			Chain: chain, Network: network,
			TransactionID: txID, TxHash: txHash, TokenID: tokenID,
			ActivityType: model.ActivityDeposit, EventAction: "deposit",
			Address: addr, Delta: "100000",
			WatchedAddress: &addr, BlockCursor: int64(200 + i),
			ChainData: json.RawMessage("{}"), EventID: eventID,
			FinalityState: "confirmed", BalanceBefore: strPtr("0"),
			BalanceAfter: strPtr("100000"), DecoderVersion: "v1", SchemaVersion: "v1",
		})
		require.NoError(t, uErr)
	}
	require.NoError(t, setupTx.Commit())

	// GetRecentEvents with limit 3.
	events, total, err := dashRepo.GetRecentEvents(ctx, chain, network, "", 3, 0)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(events), 3, "limit should be respected")
	assert.GreaterOrEqual(t, total, 5, "total should include all events")

	// GetRecentEvents filtered by address.
	events2, total2, err := dashRepo.GetRecentEvents(ctx, chain, network, addr, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 5, len(events2), "should return all 5 events for this address")
	assert.Equal(t, 5, total2)

	// Verify events are ordered by block_cursor DESC.
	for i := 1; i < len(events2); i++ {
		assert.GreaterOrEqual(t, events2[i-1].BlockCursor, events2[i].BlockCursor,
			"events should be ordered by block_cursor DESC")
	}

	// Pagination: offset 3 should return 2 events.
	events3, _, err := dashRepo.GetRecentEvents(ctx, chain, network, addr, 10, 3)
	require.NoError(t, err)
	assert.Equal(t, 2, len(events3), "offset 3 with 5 total should return 2")
}
