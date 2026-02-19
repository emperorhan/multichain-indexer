package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/google/uuid"
)

type TransactionRepo struct {
	db *DB
}

func NewTransactionRepo(db *DB) *TransactionRepo {
	return &TransactionRepo{db: db}
}

// UpsertTx inserts a transaction, returning the ID (existing or new).
func (r *TransactionRepo) UpsertTx(ctx context.Context, tx *sql.Tx, t *model.Transaction) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRowContext(ctx, `
		INSERT INTO transactions (chain, network, tx_hash, block_cursor, block_time, fee_amount, fee_payer, status, err, chain_data, block_hash, parent_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (chain, network, tx_hash) DO UPDATE SET
			chain = transactions.chain
		RETURNING id
	`, t.Chain, t.Network, t.TxHash, t.BlockCursor, t.BlockTime,
		t.FeeAmount, t.FeePayer, t.Status, t.Err, t.ChainData,
		t.BlockHash, t.ParentHash,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert transaction: %w", err)
	}
	return id, nil
}

// BulkUpsertTx inserts multiple transactions in a single multi-VALUES INSERT...ON CONFLICT RETURNING id.
// Returns a map of txHash -> uuid.UUID for all upserted transactions.
func (r *TransactionRepo) BulkUpsertTx(ctx context.Context, tx *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error) {
	if len(txns) == 0 {
		return make(map[string]uuid.UUID), nil
	}

	const cols = 12 // number of columns per row
	args := make([]interface{}, 0, len(txns)*cols)
	valuesClauses := make([]string, 0, len(txns))

	for i, t := range txns {
		base := i * cols
		valuesClauses = append(valuesClauses, fmt.Sprintf(
			"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			base+1, base+2, base+3, base+4, base+5,
			base+6, base+7, base+8, base+9, base+10,
			base+11, base+12,
		))
		args = append(args,
			t.Chain, t.Network, t.TxHash, t.BlockCursor, t.BlockTime,
			t.FeeAmount, t.FeePayer, t.Status, t.Err, t.ChainData,
			t.BlockHash, t.ParentHash,
		)
	}

	query := fmt.Sprintf(`
		INSERT INTO transactions (chain, network, tx_hash, block_cursor, block_time, fee_amount, fee_payer, status, err, chain_data, block_hash, parent_hash)
		VALUES %s
		ON CONFLICT (chain, network, tx_hash) DO UPDATE SET
			chain = transactions.chain
		RETURNING tx_hash, id
	`, strings.Join(valuesClauses, ", "))

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("bulk upsert transactions: %w", err)
	}
	defer rows.Close()

	result := make(map[string]uuid.UUID, len(txns))
	for rows.Next() {
		var txHash string
		var id uuid.UUID
		if err := rows.Scan(&txHash, &id); err != nil {
			return nil, fmt.Errorf("bulk upsert transactions scan: %w", err)
		}
		result[txHash] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("bulk upsert transactions rows: %w", err)
	}

	return result, nil
}
