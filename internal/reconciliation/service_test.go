package reconciliation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/alert"
	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// mockWatchedRepo implements store.WatchedAddressRepository.
type mockWatchedRepo struct {
	addresses []model.WatchedAddress
	err       error
}

func (m *mockWatchedRepo) GetActive(_ context.Context, _ model.Chain, _ model.Network) ([]model.WatchedAddress, error) {
	return m.addresses, m.err
}

func (m *mockWatchedRepo) Upsert(_ context.Context, _ *model.WatchedAddress) error {
	return nil
}

func (m *mockWatchedRepo) FindByAddress(_ context.Context, _ model.Chain, _ model.Network, _ string) (*model.WatchedAddress, error) {
	return nil, nil
}

// mockBalanceRepo implements store.BalanceRepository.
type mockBalanceRepo struct {
	balances map[string][]model.Balance // keyed by address
	err      error
}

func (m *mockBalanceRepo) GetByAddress(_ context.Context, _ model.Chain, _ model.Network, address string) ([]model.Balance, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.balances[address], nil
}

func (m *mockBalanceRepo) AdjustBalanceTx(_ context.Context, _ *sql.Tx, _ store.AdjustRequest) error {
	return nil
}

func (m *mockBalanceRepo) GetAmountTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, _ uuid.UUID, _ string) (string, error) {
	return "0", nil
}

func (m *mockBalanceRepo) GetAmountWithExistsTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, _ uuid.UUID, _ string) (string, bool, error) {
	return "0", false, nil
}

func (m *mockBalanceRepo) BulkGetAmountWithExistsTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
	return nil, nil
}

func (m *mockBalanceRepo) BulkAdjustBalanceTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ []store.BulkAdjustItem) error {
	return nil
}

// mockTokenRepo implements store.TokenRepository.
type mockTokenRepo struct {
	tokens map[uuid.UUID]*model.Token
}

func (m *mockTokenRepo) UpsertTx(_ context.Context, _ *sql.Tx, _ *model.Token) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (m *mockTokenRepo) BulkUpsertTx(_ context.Context, _ *sql.Tx, _ []*model.Token) (map[string]uuid.UUID, error) {
	return nil, nil
}

func (m *mockTokenRepo) FindByID(_ context.Context, id uuid.UUID) (*model.Token, error) {
	if m.tokens != nil {
		if t, ok := m.tokens[id]; ok {
			return t, nil
		}
	}
	return nil, nil
}

func (m *mockTokenRepo) FindByContractAddress(_ context.Context, _ model.Chain, _ model.Network, _ string) (*model.Token, error) {
	return nil, nil
}

func (m *mockTokenRepo) IsDeniedTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string) (bool, error) {
	return false, nil
}

func (m *mockTokenRepo) BulkIsDeniedTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ []string) (map[string]bool, error) {
	return nil, nil
}

func (m *mockTokenRepo) DenyTokenTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, _ string, _ string, _ int16, _ []string) error {
	return nil
}

func (m *mockTokenRepo) AllowTokenTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, _ string) error {
	return nil
}

// mockBalanceQueryAdapter implements chain.BalanceQueryAdapter.
type mockBalanceQueryAdapter struct {
	balances map[string]string // key: "address:tokenContract"
	err      error
}

func (m *mockBalanceQueryAdapter) Chain() string { return "solana" }

func (m *mockBalanceQueryAdapter) GetHeadSequence(_ context.Context) (int64, error) {
	return 0, nil
}

func (m *mockBalanceQueryAdapter) FetchNewSignatures(_ context.Context, _ string, _ *string, _ int) ([]chain.SignatureInfo, error) {
	return nil, nil
}

func (m *mockBalanceQueryAdapter) FetchTransactions(_ context.Context, _ []string) ([]json.RawMessage, error) {
	return nil, nil
}

func (m *mockBalanceQueryAdapter) GetBalance(_ context.Context, address, tokenContract string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	key := address + ":" + tokenContract
	bal, ok := m.balances[key]
	if !ok {
		return "0", nil
	}
	return bal, nil
}

// mockAlerter implements alert.Alerter.
type mockAlerter struct {
	mu     sync.Mutex
	alerts []alert.Alert
	err    error
}

func (m *mockAlerter) Send(_ context.Context, a alert.Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = append(m.alerts, a)
	return m.err
}

func (m *mockAlerter) getAlerts() []alert.Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]alert.Alert, len(m.alerts))
	copy(cp, m.alerts)
	return cp
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestReconcile_AllMatch(t *testing.T) {
	tokenID := uuid.New()

	watchedRepo := &mockWatchedRepo{
		addresses: []model.WatchedAddress{
			{Address: "addr1"},
		},
	}

	balanceRepo := &mockBalanceRepo{
		balances: map[string][]model.Balance{
			"addr1": {
				{TokenID: tokenID, Amount: "1000000"},
			},
		},
	}

	adapter := &mockBalanceQueryAdapter{
		balances: map[string]string{
			"addr1:": "1000000", // native token (empty contract) - matches DB
		},
	}

	alerter := &mockAlerter{}

	svc := NewService(nil, balanceRepo, watchedRepo, &mockTokenRepo{}, alerter, testLogger())
	svc.RegisterAdapter(model.ChainSolana, model.NetworkDevnet, adapter)

	result, err := svc.Reconcile(context.Background(), model.ChainSolana, model.NetworkDevnet)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.Total)
	assert.Equal(t, 1, result.Matched)
	assert.Equal(t, 0, result.Mismatched)
	assert.Equal(t, 0, result.Errors)
	assert.Equal(t, "solana", result.Chain)
	assert.Equal(t, "devnet", result.Network)

	// No alert should be sent when everything matches.
	assert.Empty(t, alerter.getAlerts())

	// Verify snapshot details
	require.Len(t, result.Snapshots, 1)
	snap := result.Snapshots[0]
	assert.True(t, snap.IsMatch)
	assert.Equal(t, "1000000", snap.OnChainBalance)
	assert.Equal(t, "1000000", snap.DBBalance)
	assert.Equal(t, "0", snap.Difference)
}

func TestReconcile_Mismatch(t *testing.T) {
	tokenID := uuid.New()

	watchedRepo := &mockWatchedRepo{
		addresses: []model.WatchedAddress{
			{Address: "addr1"},
		},
	}

	balanceRepo := &mockBalanceRepo{
		balances: map[string][]model.Balance{
			"addr1": {
				{TokenID: tokenID, Amount: "1000000"},
			},
		},
	}

	adapter := &mockBalanceQueryAdapter{
		balances: map[string]string{
			"addr1:": "2000000", // on-chain differs from DB (1000000)
		},
	}

	alerter := &mockAlerter{}

	svc := NewService(nil, balanceRepo, watchedRepo, &mockTokenRepo{}, alerter, testLogger())
	svc.RegisterAdapter(model.ChainSolana, model.NetworkDevnet, adapter)

	result, err := svc.Reconcile(context.Background(), model.ChainSolana, model.NetworkDevnet)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.Total)
	assert.Equal(t, 0, result.Matched)
	assert.Equal(t, 1, result.Mismatched)
	assert.Equal(t, 0, result.Errors)

	// Verify snapshot details
	require.Len(t, result.Snapshots, 1)
	snap := result.Snapshots[0]
	assert.False(t, snap.IsMatch)
	assert.Equal(t, "2000000", snap.OnChainBalance)
	assert.Equal(t, "1000000", snap.DBBalance)
	assert.Equal(t, "1000000", snap.Difference) // 2000000 - 1000000

	// An alert should have been sent for the mismatch.
	alerts := alerter.getAlerts()
	require.Len(t, alerts, 1)
	assert.Equal(t, alert.AlertTypeReconcileErr, alerts[0].Type)
	assert.Equal(t, "solana", alerts[0].Chain)
	assert.Equal(t, "devnet", alerts[0].Network)
	assert.Contains(t, alerts[0].Message, "mismatch")
}

func TestReconcile_OnChainError(t *testing.T) {
	tokenID := uuid.New()

	watchedRepo := &mockWatchedRepo{
		addresses: []model.WatchedAddress{
			{Address: "addr1"},
		},
	}

	balanceRepo := &mockBalanceRepo{
		balances: map[string][]model.Balance{
			"addr1": {
				{TokenID: tokenID, Amount: "500"},
			},
		},
	}

	adapter := &mockBalanceQueryAdapter{
		err: errors.New("rpc timeout"),
	}

	alerter := &mockAlerter{}

	svc := NewService(nil, balanceRepo, watchedRepo, &mockTokenRepo{}, alerter, testLogger())
	svc.RegisterAdapter(model.ChainSolana, model.NetworkDevnet, adapter)

	result, err := svc.Reconcile(context.Background(), model.ChainSolana, model.NetworkDevnet)

	require.NoError(t, err, "Reconcile should not return error for on-chain query failures")
	require.NotNil(t, result)

	// The snapshot should still be recorded, but with ERROR for on-chain balance.
	assert.Equal(t, 1, result.Total)
	require.Len(t, result.Snapshots, 1)
	snap := result.Snapshots[0]
	assert.Equal(t, "ERROR", snap.OnChainBalance)
	assert.Equal(t, "N/A", snap.Difference)
	assert.False(t, snap.IsMatch)

	// On-chain error means the balance does not "match", so it counts as mismatch.
	// The service counts non-match as mismatch (IsMatch == false).
	assert.Equal(t, 1, result.Mismatched)
}

func TestReconcile_NoAdapter(t *testing.T) {
	watchedRepo := &mockWatchedRepo{}
	balanceRepo := &mockBalanceRepo{}

	svc := NewService(nil, balanceRepo, watchedRepo, &mockTokenRepo{}, nil, testLogger())
	// No adapter registered for ethereum:mainnet

	result, err := svc.Reconcile(context.Background(), model.ChainEthereum, model.NetworkMainnet)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no balance query adapter registered")
}

func TestHasAdapter(t *testing.T) {
	svc := NewService(nil, &mockBalanceRepo{}, &mockWatchedRepo{}, &mockTokenRepo{}, nil, testLogger())

	// Before registration
	assert.False(t, svc.HasAdapter(model.ChainSolana, model.NetworkDevnet))
	assert.False(t, svc.HasAdapter(model.ChainEthereum, model.NetworkMainnet))

	// Register adapter for solana:devnet
	adapter := &mockBalanceQueryAdapter{}
	svc.RegisterAdapter(model.ChainSolana, model.NetworkDevnet, adapter)

	// After registration
	assert.True(t, svc.HasAdapter(model.ChainSolana, model.NetworkDevnet))
	assert.False(t, svc.HasAdapter(model.ChainEthereum, model.NetworkMainnet))
	assert.False(t, svc.HasAdapter(model.ChainSolana, model.NetworkMainnet))
}

func TestReconcile_NoWatchedAddresses(t *testing.T) {
	watchedRepo := &mockWatchedRepo{
		addresses: []model.WatchedAddress{}, // empty
	}
	balanceRepo := &mockBalanceRepo{}
	alerter := &mockAlerter{}

	svc := NewService(nil, balanceRepo, watchedRepo, &mockTokenRepo{}, alerter, testLogger())
	adapter := &mockBalanceQueryAdapter{}
	svc.RegisterAdapter(model.ChainSolana, model.NetworkDevnet, adapter)

	result, err := svc.Reconcile(context.Background(), model.ChainSolana, model.NetworkDevnet)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.Total)
	assert.Equal(t, 0, result.Matched)
	assert.Equal(t, 0, result.Mismatched)
	assert.Equal(t, 0, result.Errors)
	assert.Empty(t, result.Snapshots)
	assert.Empty(t, alerter.getAlerts())
}

func TestReconcile_NoDBBalances_ChecksNativeToken(t *testing.T) {
	watchedRepo := &mockWatchedRepo{
		addresses: []model.WatchedAddress{
			{Address: "addr1"},
		},
	}
	balanceRepo := &mockBalanceRepo{
		balances: map[string][]model.Balance{}, // no DB balances
	}

	adapter := &mockBalanceQueryAdapter{
		balances: map[string]string{
			"addr1:": "0", // on-chain native balance is 0
		},
	}
	alerter := &mockAlerter{}

	svc := NewService(nil, balanceRepo, watchedRepo, &mockTokenRepo{}, alerter, testLogger())
	svc.RegisterAdapter(model.ChainSolana, model.NetworkDevnet, adapter)

	result, err := svc.Reconcile(context.Background(), model.ChainSolana, model.NetworkDevnet)

	require.NoError(t, err)
	require.NotNil(t, result)
	// When DB has no balances, the service checks native token with dbAmount="0".
	assert.Equal(t, 1, result.Total)
	assert.Equal(t, 1, result.Matched)
	assert.Equal(t, 0, result.Mismatched)

	require.Len(t, result.Snapshots, 1)
	snap := result.Snapshots[0]
	assert.True(t, snap.IsMatch)
	assert.Equal(t, "0", snap.OnChainBalance)
	assert.Equal(t, "0", snap.DBBalance)
}

func TestReconcile_MultipleAddresses(t *testing.T) {
	tokenID1 := uuid.New()
	tokenID2 := uuid.New()

	watchedRepo := &mockWatchedRepo{
		addresses: []model.WatchedAddress{
			{Address: "addr1"},
			{Address: "addr2"},
		},
	}

	balanceRepo := &mockBalanceRepo{
		balances: map[string][]model.Balance{
			"addr1": {
				{TokenID: tokenID1, Amount: "100"},
			},
			"addr2": {
				{TokenID: tokenID2, Amount: "200"},
			},
		},
	}

	adapter := &mockBalanceQueryAdapter{
		balances: map[string]string{
			"addr1:": "100", // match
			"addr2:": "999", // mismatch
		},
	}

	alerter := &mockAlerter{}

	svc := NewService(nil, balanceRepo, watchedRepo, &mockTokenRepo{}, alerter, testLogger())
	svc.RegisterAdapter(model.ChainSolana, model.NetworkDevnet, adapter)

	result, err := svc.Reconcile(context.Background(), model.ChainSolana, model.NetworkDevnet)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2, result.Total)
	assert.Equal(t, 1, result.Matched)
	assert.Equal(t, 1, result.Mismatched)
	assert.Equal(t, 0, result.Errors)

	// Alert should be sent because there is at least one mismatch.
	alerts := alerter.getAlerts()
	require.Len(t, alerts, 1)
	assert.Equal(t, alert.AlertTypeReconcileErr, alerts[0].Type)
}

func TestReconcile_WatchedRepoError(t *testing.T) {
	watchedRepo := &mockWatchedRepo{
		err: errors.New("database connection failed"),
	}
	balanceRepo := &mockBalanceRepo{}

	svc := NewService(nil, balanceRepo, watchedRepo, &mockTokenRepo{}, nil, testLogger())
	svc.RegisterAdapter(model.ChainSolana, model.NetworkDevnet, &mockBalanceQueryAdapter{})

	result, err := svc.Reconcile(context.Background(), model.ChainSolana, model.NetworkDevnet)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "get watched addresses")
}
