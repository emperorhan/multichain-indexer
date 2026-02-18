package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/google/uuid"
)

type BalanceRepo struct {
	db *DB
}

func NewBalanceRepo(db *DB) *BalanceRepo {
	return &BalanceRepo{db: db}
}

// AdjustBalanceTx adjusts a balance by a delta amount within a transaction.
// Positive delta = deposit, negative delta = withdrawal/fee.
func (r *BalanceRepo) AdjustBalanceTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, address string, tokenID uuid.UUID, walletID *string, orgID *string, delta string, cursor int64, txHash string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO balances (chain, network, address, token_id, wallet_id, organization_id, amount, last_updated_cursor, last_updated_tx_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7::numeric, $8, $9)
		ON CONFLICT (chain, network, address, token_id) DO UPDATE SET
			amount = balances.amount + $7::numeric,
			last_updated_cursor = GREATEST(balances.last_updated_cursor, $8),
			last_updated_tx_hash = $9,
			updated_at = now()
	`, chain, network, address, tokenID, walletID, orgID, delta, cursor, txHash)
	if err != nil {
		return fmt.Errorf("adjust balance: %w", err)
	}
	return nil
}

func (r *BalanceRepo) GetAmountTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, address string, tokenID uuid.UUID) (string, error) {
	var amount string
	err := tx.QueryRowContext(ctx, `
		SELECT amount::text
		FROM balances
		WHERE chain = $1 AND network = $2 AND address = $3 AND token_id = $4
		FOR UPDATE
	`, chain, network, address, tokenID).Scan(&amount)
	if err != nil {
		if err == sql.ErrNoRows {
			return "0", nil
		}
		return "", fmt.Errorf("get balance amount: %w", err)
	}

	trimmed := strings.TrimSpace(amount)
	if trimmed == "" {
		return "0", nil
	}
	return trimmed, nil
}

// GetAmountWithExistsTx returns the balance amount and whether the record exists.
// If no row exists (never held), returns ("0", false, nil).
// If row exists with amount, returns (amount, true, nil).
func (r *BalanceRepo) GetAmountWithExistsTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, address string, tokenID uuid.UUID) (string, bool, error) {
	var amount string
	err := tx.QueryRowContext(ctx, `
		SELECT amount::text
		FROM balances
		WHERE chain = $1 AND network = $2 AND address = $3 AND token_id = $4
		FOR UPDATE
	`, chain, network, address, tokenID).Scan(&amount)
	if err != nil {
		if err == sql.ErrNoRows {
			return "0", false, nil
		}
		return "", false, fmt.Errorf("get balance amount with exists: %w", err)
	}

	trimmed := strings.TrimSpace(amount)
	if trimmed == "" {
		return "0", true, nil
	}
	return trimmed, true, nil
}

func (r *BalanceRepo) GetByAddress(ctx context.Context, chain model.Chain, network model.Network, address string) ([]model.Balance, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, chain, network, address, token_id, wallet_id, organization_id,
			   amount, pending_withdrawal_amount, last_updated_cursor, last_updated_tx_hash,
			   created_at, updated_at
		FROM balances
		WHERE chain = $1 AND network = $2 AND address = $3
	`, chain, network, address)
	if err != nil {
		return nil, fmt.Errorf("query balances: %w", err)
	}
	defer rows.Close()

	var balances []model.Balance
	for rows.Next() {
		var b model.Balance
		if err := rows.Scan(
			&b.ID, &b.Chain, &b.Network, &b.Address, &b.TokenID,
			&b.WalletID, &b.OrganizationID, &b.Amount,
			&b.PendingWithdrawalAmount, &b.LastUpdatedCursor,
			&b.LastUpdatedTxHash, &b.CreatedAt, &b.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan balance: %w", err)
		}
		balances = append(balances, b)
	}
	return balances, rows.Err()
}
