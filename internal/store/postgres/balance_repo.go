package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type BalanceRepo struct {
	db *DB
}

func NewBalanceRepo(db *DB) *BalanceRepo {
	return &BalanceRepo{db: db}
}

// AdjustBalanceTx adjusts a balance by a delta amount within a transaction.
// Positive delta = deposit, negative delta = withdrawal/fee.
func (r *BalanceRepo) AdjustBalanceTx(ctx context.Context, tx *sql.Tx, req store.AdjustRequest) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO balances (chain, network, address, token_id, wallet_id, organization_id, amount, last_updated_cursor, last_updated_tx_hash, balance_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7::numeric, $8, $9, $10)
		ON CONFLICT (chain, network, address, token_id, balance_type) DO UPDATE SET
			amount = balances.amount + $7::numeric,
			last_updated_cursor = GREATEST(balances.last_updated_cursor, $8),
			last_updated_tx_hash = $9,
			updated_at = now()
	`, req.Chain, req.Network, req.Address, req.TokenID, req.WalletID, req.OrgID, req.Delta, req.Cursor, req.TxHash, req.BalanceType)
	if err != nil {
		return fmt.Errorf("adjust balance: %w", err)
	}
	return nil
}

func (r *BalanceRepo) GetByAddress(ctx context.Context, chain model.Chain, network model.Network, address string) ([]model.Balance, error) {
	ctx, cancel := withTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, chain, network, address, token_id, balance_type, wallet_id, organization_id,
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
			&b.ID, &b.Chain, &b.Network, &b.Address, &b.TokenID, &b.BalanceType,
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

// BulkGetAmountWithExistsTx returns the balance amounts and existence flags for multiple keys
// in a single query. No row-level locking needed: single-writer ingester pattern
// and BulkAdjustBalanceTx's ON CONFLICT guarantee atomicity.
// Keys not found in the DB are returned with Amount="0" and Exists=false.
func (r *BalanceRepo) BulkGetAmountWithExistsTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, keys []store.BalanceKey) (map[store.BalanceKey]store.BalanceInfo, error) {
	result := make(map[store.BalanceKey]store.BalanceInfo, len(keys))
	if len(keys) == 0 {
		return result, nil
	}

	// Initialize all keys with default (not exists).
	for _, k := range keys {
		result[k] = store.BalanceInfo{Amount: "0", Exists: false}
	}

	// Build a dynamic WHERE clause: (address, token_id, balance_type) IN ((...), (...), ...)
	const cols = 3 // address, token_id, balance_type per tuple
	args := make([]interface{}, 0, 2+len(keys)*cols) // chain + network + tuples
	args = append(args, chain, network)

	var sb strings.Builder
	sb.Grow(len(keys) * 20)
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(", ")
		}
		base := 2 + i*cols
		sb.WriteByte('(')
		sb.WriteByte('$')
		sb.WriteString(strconv.Itoa(base + 1))
		sb.WriteString(", $")
		sb.WriteString(strconv.Itoa(base + 2))
		sb.WriteString(", $")
		sb.WriteString(strconv.Itoa(base + 3))
		sb.WriteByte(')')
		args = append(args, k.Address, k.TokenID, k.BalanceType)
	}

	query := fmt.Sprintf(`
		SELECT address, token_id, balance_type, amount::text
		FROM balances
		WHERE chain = $1 AND network = $2
		  AND (address, token_id, balance_type) IN (%s)
	`, sb.String())

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("bulk get balance amounts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var address string
		var tokenID uuid.UUID
		var balanceType string
		var amount string
		if err := rows.Scan(&address, &tokenID, &balanceType, &amount); err != nil {
			return nil, fmt.Errorf("bulk get balance amounts scan: %w", err)
		}
		trimmed := strings.TrimSpace(amount)
		if trimmed == "" {
			trimmed = "0"
		}
		key := store.BalanceKey{Address: address, TokenID: tokenID, BalanceType: balanceType}
		result[key] = store.BalanceInfo{Amount: trimmed, Exists: true}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("bulk get balance amounts rows: %w", err)
	}

	return result, nil
}

// BulkAdjustBalanceTx adjusts multiple balances in a single UNNEST-based INSERT...ON CONFLICT query.
func (r *BalanceRepo) BulkAdjustBalanceTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, items []store.BulkAdjustItem) error {
	if len(items) == 0 {
		return nil
	}

	// Build parallel arrays for UNNEST.
	addresses := make([]string, len(items))
	tokenIDs := make([]uuid.UUID, len(items))
	walletIDs := make([]*string, len(items))
	orgIDs := make([]*string, len(items))
	deltas := make([]string, len(items))
	cursors := make([]int64, len(items))
	txHashes := make([]string, len(items))
	balanceTypes := make([]string, len(items))

	for i, item := range items {
		addresses[i] = item.Address
		tokenIDs[i] = item.TokenID
		walletIDs[i] = item.WalletID
		orgIDs[i] = item.OrgID
		deltas[i] = item.Delta
		cursors[i] = item.Cursor
		txHashes[i] = item.TxHash
		balanceTypes[i] = item.BalanceType
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO balances (chain, network, address, token_id, wallet_id, organization_id, amount, last_updated_cursor, last_updated_tx_hash, balance_type)
		SELECT $1, $2,
			unnest($3::text[]),
			unnest($4::uuid[]),
			unnest($5::text[]),
			unnest($6::text[]),
			GREATEST(0, unnest($7::numeric[])),
			unnest($8::bigint[]),
			unnest($9::text[]),
			unnest($10::text[])
		ON CONFLICT (chain, network, address, token_id, balance_type) DO UPDATE SET
			amount = GREATEST(0, balances.amount + EXCLUDED.amount),
			last_updated_cursor = GREATEST(balances.last_updated_cursor, EXCLUDED.last_updated_cursor),
			last_updated_tx_hash = EXCLUDED.last_updated_tx_hash,
			updated_at = now()
	`, chain, network,
		pq.Array(addresses),
		pq.Array(tokenIDs),
		pq.Array(walletIDs),
		pq.Array(orgIDs),
		pq.Array(deltas),
		pq.Array(cursors),
		pq.Array(txHashes),
		pq.Array(balanceTypes),
	)
	if err != nil {
		return fmt.Errorf("bulk adjust balances: %w", err)
	}
	return nil
}
