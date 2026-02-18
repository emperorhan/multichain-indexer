package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/google/uuid"
)

type BalanceEventRepo struct {
	db *DB
}

func NewBalanceEventRepo(db *DB) *BalanceEventRepo {
	return &BalanceEventRepo{db: db}
}

func (r *BalanceEventRepo) UpsertTx(ctx context.Context, tx *sql.Tx, be *model.BalanceEvent) (bool, error) {
	if be.EventID == "" {
		return false, fmt.Errorf("upsert balance event: event_id is required")
	}

	canonicalID, err := r.stableCanonicalEventID(ctx, tx, be.Chain, be.Network, be.EventID)
	if err != nil {
		return false, fmt.Errorf("upsert balance event: canonical event id: %w", err)
	}

	updated, err := r.updateExistingByCanonicalID(ctx, tx, canonicalID, be)
	if err != nil {
		return false, fmt.Errorf("upsert balance event: update existing: %w", err)
	}
	if updated {
		return false, nil
	}

	exists, err := r.eventByCanonicalIDExists(ctx, tx, canonicalID)
	if err != nil {
		return false, fmt.Errorf("upsert balance event: exists check: %w", err)
	}
	if exists {
		// Canonical row already exists, but this upsert could not advance finality.
		return false, nil
	}

	inserted, err := r.insertByCanonicalID(ctx, tx, canonicalID, be)
	if err != nil {
		return false, err
	}

	return inserted, nil
}

func (r *BalanceEventRepo) stableCanonicalEventID(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, eventID string) (uuid.UUID, error) {
	const sqlAcquire = `
		INSERT INTO balance_event_canonical_ids (
			chain, network, event_id, balance_event_id
		)
		VALUES ($1, $2, $3, gen_random_uuid())
		ON CONFLICT (chain, network, event_id)
		DO UPDATE SET updated_at = now()
		RETURNING balance_event_id
	`
	var canonicalID uuid.UUID
	if err := tx.QueryRowContext(ctx, sqlAcquire, chain, network, eventID).Scan(&canonicalID); err != nil {
		return uuid.Nil, fmt.Errorf("resolve canonical id: %w", err)
	}

	// Serialize all updates for this canonical identity to prevent partition drift duplicates.
	const sqlLock = `
		SELECT balance_event_id
		FROM balance_event_canonical_ids
		WHERE chain = $1 AND network = $2 AND event_id = $3
		FOR UPDATE
	`
	var lockedID uuid.UUID
	if err := tx.QueryRowContext(ctx, sqlLock, chain, network, eventID).Scan(&lockedID); err != nil {
		return uuid.Nil, fmt.Errorf("lock canonical id row: %w", err)
	}
	if lockedID != canonicalID {
		return uuid.Nil, fmt.Errorf("canonical id race detected for %s/%s/%s", chain, network, eventID)
	}

	return canonicalID, nil
}

func (r *BalanceEventRepo) updateExistingByCanonicalID(ctx context.Context, tx *sql.Tx, canonicalID uuid.UUID, be *model.BalanceEvent) (bool, error) {
	const sqlUpdate = `
		UPDATE balance_events
		SET
			finality_state = $4,
			block_hash = COALESCE(NULLIF($5, ''), balance_events.block_hash),
			tx_index = CASE
				WHEN $6 <> 0 THEN $6
				ELSE balance_events.tx_index
			END,
			block_cursor = GREATEST(balance_events.block_cursor, $7),
			-- Avoid moving canonical rows backward across partition boundaries under block-time perturbation.
			block_time = GREATEST(balance_events.block_time, COALESCE($8, balance_events.block_time)),
			chain_data = COALESCE($9, balance_events.chain_data),
			decoder_version = COALESCE(NULLIF($10, ''), balance_events.decoder_version),
			schema_version = COALESCE(NULLIF($11, ''), balance_events.schema_version)
		WHERE id = $1
		  AND chain = $2
		  AND network = $3
		  AND
			CASE LOWER(COALESCE(TRIM($4), ''))
				WHEN 'finalized' THEN 4
				WHEN 'safe' THEN 3
				WHEN 'confirmed' THEN 2
				WHEN 'processed' THEN 1
				WHEN 'latest' THEN 1
				WHEN 'pending' THEN 1
				WHEN 'unsafe' THEN 1
				ELSE 0
			END >
			CASE LOWER(COALESCE(TRIM(balance_events.finality_state), ''))
				WHEN 'finalized' THEN 4
				WHEN 'safe' THEN 3
				WHEN 'confirmed' THEN 2
				WHEN 'processed' THEN 1
				WHEN 'latest' THEN 1
				WHEN 'pending' THEN 1
				WHEN 'unsafe' THEN 1
				ELSE 0
			END
	`

	result, err := tx.ExecContext(
		ctx,
		sqlUpdate,
		canonicalID,
		be.Chain,
		be.Network,
		be.FinalityState,
		be.BlockHash,
		be.TxIndex,
		be.BlockCursor,
		be.BlockTime,
		be.ChainData,
		be.DecoderVersion,
		be.SchemaVersion,
	)
	if err != nil {
		return false, fmt.Errorf("update existing event by canonical id: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read update rows: %w", err)
	}
	if rows > 0 {
		return true, nil
	}

	return false, nil
}

func (r *BalanceEventRepo) eventByCanonicalIDExists(ctx context.Context, tx *sql.Tx, canonicalID uuid.UUID) (bool, error) {
	const sqlExists = `
		SELECT EXISTS(
			SELECT 1 FROM balance_events WHERE id = $1
		)
	`
	var exists bool
	if err := tx.QueryRowContext(ctx, sqlExists, canonicalID).Scan(&exists); err != nil {
		return false, fmt.Errorf("query canonical event existence: %w", err)
	}
	return exists, nil
}

func (r *BalanceEventRepo) insertByCanonicalID(ctx context.Context, tx *sql.Tx, canonicalID uuid.UUID, be *model.BalanceEvent) (bool, error) {
	const sqlInsert = `
		INSERT INTO balance_events (
			id, chain, network, transaction_id, tx_hash,
			outer_instruction_index, inner_instruction_index,
			token_id, event_category, event_action, program_id,
			address, counterparty_address, delta, balance_before, balance_after,
			watched_address, wallet_id, organization_id,
			block_cursor, block_time, chain_data,
			event_id, block_hash, tx_index, event_path, event_path_type,
			actor_address, asset_type, asset_id,
			finality_state, decoder_version, schema_version
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33)
		ON CONFLICT (id, block_time) DO UPDATE
		SET
			finality_state = EXCLUDED.finality_state,
			block_hash = COALESCE(NULLIF(EXCLUDED.block_hash, ''), balance_events.block_hash),
			tx_index = CASE
				WHEN EXCLUDED.tx_index <> 0 THEN EXCLUDED.tx_index
			ELSE balance_events.tx_index
			END,
			block_cursor = GREATEST(balance_events.block_cursor, EXCLUDED.block_cursor),
			-- Keep deterministic partition targeting by monotonic block-time progression per canonical identity.
			block_time = GREATEST(balance_events.block_time, COALESCE(EXCLUDED.block_time, balance_events.block_time)),
			chain_data = COALESCE(EXCLUDED.chain_data, balance_events.chain_data),
			decoder_version = COALESCE(NULLIF(EXCLUDED.decoder_version, ''), balance_events.decoder_version),
			schema_version = COALESCE(NULLIF(EXCLUDED.schema_version, ''), balance_events.schema_version)
		WHERE
			CASE LOWER(COALESCE(TRIM(EXCLUDED.finality_state), ''))
				WHEN 'finalized' THEN 4
				WHEN 'safe' THEN 3
				WHEN 'confirmed' THEN 2
				WHEN 'processed' THEN 1
				WHEN 'latest' THEN 1
				WHEN 'pending' THEN 1
				WHEN 'unsafe' THEN 1
				ELSE 0
			END >
			CASE LOWER(COALESCE(TRIM(balance_events.finality_state), ''))
				WHEN 'finalized' THEN 4
				WHEN 'safe' THEN 3
				WHEN 'confirmed' THEN 2
				WHEN 'processed' THEN 1
				WHEN 'latest' THEN 1
				WHEN 'pending' THEN 1
				WHEN 'unsafe' THEN 1
				ELSE 0
			END
		RETURNING (xmax = 0) AS inserted
	`
	var inserted bool
	err := tx.QueryRowContext(
		ctx,
		sqlInsert,
		canonicalID,
		be.Chain,
		be.Network,
		be.TransactionID,
		be.TxHash,
		be.OuterInstructionIndex,
		be.InnerInstructionIndex,
		be.TokenID,
		be.EventCategory,
		be.EventAction,
		be.ProgramID,
		be.Address,
		be.CounterpartyAddress,
		be.Delta,
		be.BalanceBefore,
		be.BalanceAfter,
		be.WatchedAddress,
		be.WalletID,
		be.OrganizationID,
		be.BlockCursor,
		be.BlockTime,
		be.ChainData,
		be.EventID,
		be.BlockHash,
		be.TxIndex,
		be.EventPath,
		be.EventPathType,
		be.ActorAddress,
		be.AssetType,
		be.AssetID,
		be.FinalityState,
		be.DecoderVersion,
		be.SchemaVersion,
	).Scan(&inserted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("insert event by canonical id: %w", err)
	}
	return inserted, nil
}
