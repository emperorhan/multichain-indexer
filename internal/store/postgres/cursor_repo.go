package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

type CursorRepo struct {
	db *DB
}

func NewCursorRepo(db *DB) *CursorRepo {
	return &CursorRepo{db: db}
}

func (r *CursorRepo) Get(ctx context.Context, chain model.Chain, network model.Network, address string) (*model.AddressCursor, error) {
	var c model.AddressCursor
	err := r.db.QueryRowContext(ctx, `
		SELECT id, chain, network, address, cursor_value, cursor_sequence, items_processed, last_fetched_at, created_at, updated_at
		FROM address_cursors
		WHERE chain = $1 AND network = $2 AND address = $3
	`, chain, network, address).Scan(
		&c.ID, &c.Chain, &c.Network, &c.Address,
		&c.CursorValue, &c.CursorSequence, &c.ItemsProcessed,
		&c.LastFetchedAt, &c.CreatedAt, &c.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cursor: %w", err)
	}
	return &c, nil
}

func (r *CursorRepo) UpsertTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, address string, cursorValue *string, cursorSequence int64, itemsProcessed int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO address_cursors (chain, network, address, cursor_value, cursor_sequence, items_processed, last_fetched_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (chain, network, address) DO UPDATE SET
			cursor_value = EXCLUDED.cursor_value,
			cursor_sequence = EXCLUDED.cursor_sequence,
			items_processed = address_cursors.items_processed + EXCLUDED.items_processed,
			last_fetched_at = now(),
			updated_at = now()
	`, chain, network, address, cursorValue, cursorSequence, itemsProcessed)
	if err != nil {
		return fmt.Errorf("upsert cursor: %w", err)
	}
	return nil
}

func (r *CursorRepo) EnsureExists(ctx context.Context, chain model.Chain, network model.Network, address string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO address_cursors (chain, network, address)
		VALUES ($1, $2, $3)
		ON CONFLICT (chain, network, address) DO NOTHING
	`, chain, network, address)
	if err != nil {
		return fmt.Errorf("ensure cursor exists: %w", err)
	}
	return nil
}
