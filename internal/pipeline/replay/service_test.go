package replay

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/google/uuid"
)

// --- Mock implementations ---

type mockTxBeginner struct {
	err error
}

func (m *mockTxBeginner) BeginTx(_ context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
	return nil, m.err
}

type mockBalanceRepo struct {
	adjustCalled int
}

func (m *mockBalanceRepo) AdjustBalanceTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ string, _ uuid.UUID, _ *string, _ *string, _ string, _ int64, _ string, _ string) error {
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

func (m *mockBalanceRepo) BulkAdjustBalanceTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ []store.BulkAdjustItem) error {
	return nil
}

type mockConfigRepo struct {
	watermark *model.PipelineWatermark
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

func (m *mockConfigRepo) GetWatermark(_ context.Context, _ model.Chain, _ model.Network) (*model.PipelineWatermark, error) {
	return m.watermark, nil
}

type mockBlockRepo struct {
	block            *model.IndexedBlock
	getErr           error
	deleteFromCalled bool
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

func (m *mockBlockRepo) DeleteFromBlockTx(_ context.Context, _ *sql.Tx, _ model.Chain, _ model.Network, _ int64) error {
	m.deleteFromCalled = true
	return nil
}

func (m *mockBlockRepo) PurgeFinalizedBefore(_ context.Context, _ model.Chain, _ model.Network, _ int64) (int64, error) {
	return 0, nil
}

func testLogger() *slog.Logger {
	return slog.Default()
}

// --- Helper function tests ---

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
