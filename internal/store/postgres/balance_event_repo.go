package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

type BalanceEventRepo struct {
	db *DB
}

func NewBalanceEventRepo(db *DB) *BalanceEventRepo {
	return &BalanceEventRepo{db: db}
}

func (r *BalanceEventRepo) UpsertTx(ctx context.Context, tx *sql.Tx, be *model.BalanceEvent) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO balance_events (
			chain, network, transaction_id, tx_hash,
			outer_instruction_index, inner_instruction_index,
			token_id, event_category, event_action, program_id,
			address, counterparty_address, delta,
			watched_address, wallet_id, organization_id,
			block_cursor, block_time, chain_data
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (chain, network, tx_hash, outer_instruction_index,
		             inner_instruction_index, address, watched_address) DO NOTHING
	`, be.Chain, be.Network, be.TransactionID, be.TxHash,
		be.OuterInstructionIndex, be.InnerInstructionIndex,
		be.TokenID, be.EventCategory, be.EventAction, be.ProgramID,
		be.Address, be.CounterpartyAddress, be.Delta,
		be.WatchedAddress, be.WalletID, be.OrganizationID,
		be.BlockCursor, be.BlockTime, be.ChainData,
	)
	if err != nil {
		return fmt.Errorf("upsert balance event: %w", err)
	}
	return nil
}
