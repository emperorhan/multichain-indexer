package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
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
		INSERT INTO transactions (chain, network, tx_hash, block_cursor, block_time, fee_amount, fee_payer, status, err, chain_data)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (chain, network, tx_hash) DO UPDATE SET
			chain = transactions.chain
		RETURNING id
	`, t.Chain, t.Network, t.TxHash, t.BlockCursor, t.BlockTime,
		t.FeeAmount, t.FeePayer, t.Status, t.Err, t.ChainData,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert transaction: %w", err)
	}
	return id, nil
}
