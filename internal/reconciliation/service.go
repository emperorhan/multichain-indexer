package reconciliation

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/alert"
	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

// SnapshotResult holds the result of a single address+token reconciliation.
type SnapshotResult struct {
	Chain          string    `json:"chain"`
	Network        string    `json:"network"`
	Address        string    `json:"address"`
	TokenContract  string    `json:"token_contract"`
	OnChainBalance string    `json:"on_chain_balance"`
	DBBalance      string    `json:"db_balance"`
	Difference     string    `json:"difference"`
	IsMatch        bool      `json:"is_match"`
	CheckedAt      time.Time `json:"checked_at"`
}

// RunResult aggregates a full reconciliation run.
type RunResult struct {
	Chain      string           `json:"chain"`
	Network    string           `json:"network"`
	Total      int              `json:"total"`
	Matched    int              `json:"matched"`
	Mismatched int              `json:"mismatched"`
	Errors     int              `json:"errors"`
	Snapshots  []SnapshotResult `json:"snapshots"`
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt time.Time        `json:"finished_at"`
}

// Service performs balance reconciliation between on-chain and DB state.
type Service struct {
	db          *sql.DB
	balanceRepo store.BalanceRepository
	watchedRepo store.WatchedAddressRepository
	tokenRepo   store.TokenRepository
	adapters    map[string]chain.BalanceQueryAdapter // keyed by "chain:network"
	alerter     alert.Alerter
	logger      *slog.Logger
	snapshotRepo SnapshotRepository
}

// SnapshotRepository persists reconciliation results.
type SnapshotRepository interface {
	SaveSnapshots(ctx context.Context, tx *sql.Tx, snapshots []SnapshotResult) error
}

// NewService creates a new reconciliation service.
func NewService(
	db *sql.DB,
	balanceRepo store.BalanceRepository,
	watchedRepo store.WatchedAddressRepository,
	tokenRepo store.TokenRepository,
	alerter alert.Alerter,
	logger *slog.Logger,
) *Service {
	return &Service{
		db:          db,
		balanceRepo: balanceRepo,
		watchedRepo: watchedRepo,
		tokenRepo:   tokenRepo,
		adapters:    make(map[string]chain.BalanceQueryAdapter),
		alerter:     alerter,
		logger:      logger.With("component", "reconciliation"),
	}
}

// SetSnapshotRepository sets the optional snapshot persistence layer.
func (s *Service) SetSnapshotRepository(repo SnapshotRepository) {
	s.snapshotRepo = repo
}

// RegisterAdapter registers a chain adapter that supports balance queries.
func (s *Service) RegisterAdapter(ch model.Chain, net model.Network, adapter chain.BalanceQueryAdapter) {
	key := string(ch) + ":" + string(net)
	s.adapters[key] = adapter
}

// Reconcile runs reconciliation for the given chain/network.
func (s *Service) Reconcile(ctx context.Context, ch model.Chain, net model.Network) (*RunResult, error) {
	key := string(ch) + ":" + string(net)
	adapter, ok := s.adapters[key]
	if !ok {
		return nil, fmt.Errorf("no balance query adapter registered for %s", key)
	}

	result := &RunResult{
		Chain:     string(ch),
		Network:   string(net),
		StartedAt: time.Now(),
	}

	// Get watched addresses
	addresses, err := s.watchedRepo.GetActive(ctx, ch, net)
	if err != nil {
		return nil, fmt.Errorf("get watched addresses: %w", err)
	}

	for _, wa := range addresses {
		// Get DB balances for this address
		dbBalances, err := s.balanceRepo.GetByAddress(ctx, ch, net, wa.Address)
		if err != nil {
			s.logger.Warn("failed to get DB balances", "address", wa.Address, "error", err)
			result.Errors++
			continue
		}

		if len(dbBalances) == 0 {
			// Check native token balance
			snap := s.reconcileOne(ctx, adapter, ch, net, wa.Address, "", "0")
			result.Snapshots = append(result.Snapshots, snap)
			result.Total++
			if snap.IsMatch {
				result.Matched++
			} else {
				result.Mismatched++
			}
			continue
		}

		for _, bal := range dbBalances {
			// Native token uses empty contract address
			tokenContract := ""
			// For non-native tokens, look up the contract address from token_id
			if token, err := s.findTokenContract(ctx, ch, net, bal.TokenID.String()); err == nil {
				tokenContract = token
			}

			snap := s.reconcileOne(ctx, adapter, ch, net, wa.Address, tokenContract, bal.Amount)
			result.Snapshots = append(result.Snapshots, snap)
			result.Total++
			if snap.IsMatch {
				result.Matched++
			} else {
				result.Mismatched++
			}
		}
	}

	result.FinishedAt = time.Now()

	metrics.ReconciliationRunsTotal.WithLabelValues(string(ch), string(net)).Inc()
	if result.Mismatched > 0 {
		metrics.ReconciliationMismatchesTotal.WithLabelValues(string(ch), string(net)).Add(float64(result.Mismatched))

		if s.alerter != nil {
			_ = s.alerter.Send(ctx, alert.Alert{
				Type:    alert.AlertTypeReconcileErr,
				Chain:   string(ch),
				Network: string(net),
				Title:   "Balance reconciliation mismatch detected",
				Message: fmt.Sprintf("%d/%d addresses have balance mismatch", result.Mismatched, result.Total),
				Fields: map[string]string{
					"matched":    fmt.Sprintf("%d", result.Matched),
					"mismatched": fmt.Sprintf("%d", result.Mismatched),
					"errors":     fmt.Sprintf("%d", result.Errors),
				},
			})
		}
	}

	// Persist snapshots if repo is configured
	if s.snapshotRepo != nil && len(result.Snapshots) > 0 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			s.logger.Warn("failed to begin tx for snapshot persistence", "error", err)
		} else {
			if err := s.snapshotRepo.SaveSnapshots(ctx, tx, result.Snapshots); err != nil {
				_ = tx.Rollback()
				s.logger.Warn("failed to save reconciliation snapshots", "error", err)
			} else {
				_ = tx.Commit()
			}
		}
	}

	s.logger.Info("reconciliation completed",
		"chain", ch, "network", net,
		"total", result.Total, "matched", result.Matched,
		"mismatched", result.Mismatched, "errors", result.Errors,
	)

	return result, nil
}

func (s *Service) reconcileOne(
	ctx context.Context,
	adapter chain.BalanceQueryAdapter,
	ch model.Chain, net model.Network,
	address, tokenContract, dbAmount string,
) SnapshotResult {
	snap := SnapshotResult{
		Chain:         string(ch),
		Network:       string(net),
		Address:       address,
		TokenContract: tokenContract,
		DBBalance:     dbAmount,
		CheckedAt:     time.Now(),
	}

	onChain, err := adapter.GetBalance(ctx, address, tokenContract)
	if err != nil {
		s.logger.Warn("on-chain balance query failed",
			"address", address, "token", tokenContract, "error", err)
		snap.OnChainBalance = "ERROR"
		snap.Difference = "N/A"
		return snap
	}

	snap.OnChainBalance = onChain

	// Compare
	onChainBig, ok1 := new(big.Int).SetString(onChain, 10)
	dbBig, ok2 := new(big.Int).SetString(dbAmount, 10)

	if ok1 && ok2 {
		diff := new(big.Int).Sub(onChainBig, dbBig)
		snap.Difference = diff.String()
		snap.IsMatch = diff.Sign() == 0
	} else {
		snap.Difference = "PARSE_ERROR"
		snap.IsMatch = onChain == dbAmount
	}

	return snap
}

// findTokenContract returns the contract address for a token ID.
// Returns empty string for native tokens or on error.
func (s *Service) findTokenContract(_ context.Context, _ model.Chain, _ model.Network, _ string) (string, error) {
	// Token contract lookup from token_id requires a FindByID method.
	// For now, native balance reconciliation uses empty contract.
	// TODO: Add TokenRepository.FindByID for full token contract resolution.
	return "", nil
}

// HasAdapter returns true if a balance query adapter is registered for the chain/network.
func (s *Service) HasAdapter(ch model.Chain, net model.Network) bool {
	key := string(ch) + ":" + string(net)
	_, ok := s.adapters[key]
	return ok
}

// ReconcileAny wraps Reconcile to return any, satisfying admin.ReconcileRequester.
func (s *Service) ReconcileAny(ctx context.Context, ch model.Chain, net model.Network) (any, error) {
	return s.Reconcile(ctx, ch, net)
}
