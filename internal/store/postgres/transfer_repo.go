package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kodax/koda-custody-indexer/internal/domain/model"
)

type TransferRepo struct {
	db *DB
}

func NewTransferRepo(db *DB) *TransferRepo {
	return &TransferRepo{db: db}
}

func (r *TransferRepo) UpsertTx(ctx context.Context, tx *sql.Tx, t *model.Transfer) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO transfers (
			chain, network, transaction_id, tx_hash, instruction_index,
			token_id, from_address, to_address, amount, direction,
			watched_address, wallet_id, organization_id, block_cursor, block_time, chain_data
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (chain, network, tx_hash, instruction_index, watched_address) DO NOTHING
	`, t.Chain, t.Network, t.TransactionID, t.TxHash, t.InstructionIndex,
		t.TokenID, t.FromAddress, t.ToAddress, t.Amount, t.Direction,
		t.WatchedAddress, t.WalletID, t.OrganizationID, t.BlockCursor, t.BlockTime, t.ChainData,
	)
	if err != nil {
		return fmt.Errorf("upsert transfer: %w", err)
	}
	return nil
}
