package postgres

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/emperorhan/multichain-indexer/internal/reconciliation"
)

// ReconciliationSnapshotRepo implements reconciliation.SnapshotRepository.
type ReconciliationSnapshotRepo struct {
	db *sql.DB
}

// NewReconciliationSnapshotRepo creates a new reconciliation snapshot repository.
func NewReconciliationSnapshotRepo(db *sql.DB) *ReconciliationSnapshotRepo {
	return &ReconciliationSnapshotRepo{db: db}
}

// SaveSnapshots persists reconciliation results in batch.
func (r *ReconciliationSnapshotRepo) SaveSnapshots(ctx context.Context, tx *sql.Tx, snapshots []reconciliation.SnapshotResult) error {
	if len(snapshots) == 0 {
		return nil
	}

	const batchSize = 1000
	for i := 0; i < len(snapshots); i += batchSize {
		end := i + batchSize
		if end > len(snapshots) {
			end = len(snapshots)
		}
		if err := r.insertBatch(ctx, tx, snapshots[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconciliationSnapshotRepo) insertBatch(ctx context.Context, tx *sql.Tx, snapshots []reconciliation.SnapshotResult) error {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO balance_reconciliation_snapshots
		(chain, network, address, token_contract, on_chain_balance, db_balance, difference, is_match, checked_at)
		VALUES `)

	args := make([]any, 0, len(snapshots)*9)
	for i, snap := range snapshots {
		if i > 0 {
			sb.WriteString(",")
		}
		base := i * 9
		sb.WriteString("($")
		sb.WriteString(strconv.Itoa(base + 1))
		sb.WriteString(",$")
		sb.WriteString(strconv.Itoa(base + 2))
		sb.WriteString(",$")
		sb.WriteString(strconv.Itoa(base + 3))
		sb.WriteString(",$")
		sb.WriteString(strconv.Itoa(base + 4))
		sb.WriteString(",$")
		sb.WriteString(strconv.Itoa(base + 5))
		sb.WriteString(",$")
		sb.WriteString(strconv.Itoa(base + 6))
		sb.WriteString(",$")
		sb.WriteString(strconv.Itoa(base + 7))
		sb.WriteString(",$")
		sb.WriteString(strconv.Itoa(base + 8))
		sb.WriteString(",$")
		sb.WriteString(strconv.Itoa(base + 9))
		sb.WriteString(")")
		args = append(args,
			snap.Chain, snap.Network, snap.Address, snap.TokenContract,
			snap.OnChainBalance, snap.DBBalance, snap.Difference, snap.IsMatch, snap.CheckedAt)
	}

	_, err := tx.ExecContext(ctx, sb.String(), args...)
	return err
}
