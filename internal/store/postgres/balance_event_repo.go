package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/google/uuid"
)

type BalanceEventRepo struct {
	db *DB
}

func NewBalanceEventRepo(db *DB) *BalanceEventRepo {
	return &BalanceEventRepo{db: db}
}

func (r *BalanceEventRepo) UpsertTx(ctx context.Context, tx *sql.Tx, be *model.BalanceEvent) (store.UpsertResult, error) {
	if be.EventID == "" {
		return store.UpsertResult{}, fmt.Errorf("upsert balance event: event_id is required")
	}

	canonicalID, err := r.stableCanonicalEventID(ctx, tx, be.Chain, be.Network, be.EventID)
	if err != nil {
		return store.UpsertResult{}, fmt.Errorf("upsert balance event: canonical event id: %w", err)
	}

	updated, finalityCrossed, err := r.updateExistingByCanonicalID(ctx, tx, canonicalID, be)
	if err != nil {
		return store.UpsertResult{}, fmt.Errorf("upsert balance event: update existing: %w", err)
	}
	if updated {
		return store.UpsertResult{FinalityCrossed: finalityCrossed}, nil
	}

	exists, err := r.eventByCanonicalIDExists(ctx, tx, be.Chain, be.Network, be.EventID)
	if err != nil {
		return store.UpsertResult{}, fmt.Errorf("upsert balance event: exists check: %w", err)
	}
	if exists {
		return store.UpsertResult{}, nil
	}

	inserted, err := r.insertByCanonicalID(ctx, tx, canonicalID, be)
	if err != nil {
		return store.UpsertResult{}, err
	}

	return store.UpsertResult{Inserted: inserted}, nil
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

func (r *BalanceEventRepo) updateExistingByCanonicalID(ctx context.Context, tx *sql.Tx, canonicalID uuid.UUID, be *model.BalanceEvent) (updated bool, finalityCrossed bool, err error) {
	// First, check existing balance_applied state before update.
	var existingBalanceApplied bool
	checkErr := tx.QueryRowContext(ctx, `
		SELECT balance_applied FROM balance_events
		WHERE id = $1 AND chain = $2 AND network = $3
	`, canonicalID, be.Chain, be.Network).Scan(&existingBalanceApplied)
	if checkErr != nil && !errors.Is(checkErr, sql.ErrNoRows) {
		return false, false, fmt.Errorf("check existing balance_applied: %w", checkErr)
	}
	existingRowFound := checkErr == nil

	const sqlUpdate = `
		UPDATE balance_events
		SET
			finality_state = $4,
			balance_applied = CASE
				WHEN $12::boolean AND NOT balance_events.balance_applied THEN true
				ELSE balance_events.balance_applied
			END,
			block_hash = COALESCE(NULLIF($5, ''), balance_events.block_hash),
			tx_index = CASE
				WHEN $6 <> 0 THEN $6
				ELSE balance_events.tx_index
			END,
			block_cursor = GREATEST(balance_events.block_cursor, $7),
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
			END >=
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

	result, execErr := tx.ExecContext(
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
		be.BalanceApplied,
	)
	if execErr != nil {
		return false, false, fmt.Errorf("update existing event by canonical id: %w", execErr)
	}

	rows, rowErr := result.RowsAffected()
	if rowErr != nil {
		return false, false, fmt.Errorf("read update rows: %w", rowErr)
	}
	if rows > 0 {
		crossed := existingRowFound && !existingBalanceApplied && be.BalanceApplied
		return true, crossed, nil
	}

	return false, false, nil
}

func (r *BalanceEventRepo) eventByCanonicalIDExists(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, eventID string) (bool, error) {
	const sqlExists = `
		SELECT EXISTS(
			SELECT 1 FROM balance_events WHERE chain = $1 AND network = $2 AND event_id = $3
		)
	`
	var exists bool
	if err := tx.QueryRowContext(ctx, sqlExists, chain, network, eventID).Scan(&exists); err != nil {
		return false, fmt.Errorf("query canonical event existence: %w", err)
	}
	return exists, nil
}

func (r *BalanceEventRepo) insertByCanonicalID(ctx context.Context, tx *sql.Tx, canonicalID uuid.UUID, be *model.BalanceEvent) (bool, error) {
	const sqlInsert = `
		INSERT INTO balance_events (
			id, chain, network, transaction_id, tx_hash,
			outer_instruction_index, inner_instruction_index,
			token_id, activity_type, event_action, program_id,
			address, counterparty_address, delta, balance_before, balance_after,
			watched_address, wallet_id, organization_id,
			block_cursor, block_time, chain_data, balance_applied,
			event_id, block_hash, tx_index, event_path, event_path_type,
			actor_address, asset_type, asset_id,
			finality_state, decoder_version, schema_version
		) SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34
		WHERE NOT EXISTS (
			SELECT 1
			FROM balance_events
			WHERE chain = $2
				AND network = $3
				AND event_id = $24
		)
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
		be.ActivityType,
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
		be.BalanceApplied,
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

// BulkUpsertTx upserts multiple balance events by delegating to UpsertTx for each event
// and aggregating the results.
func (r *BalanceEventRepo) BulkUpsertTx(ctx context.Context, tx *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
	if len(events) == 0 {
		return store.BulkUpsertEventResult{}, nil
	}

	var result store.BulkUpsertEventResult
	for _, ev := range events {
		ur, err := r.UpsertTx(ctx, tx, ev)
		if err != nil {
			return result, fmt.Errorf("bulk upsert balance event (event_id=%s): %w", ev.EventID, err)
		}
		if ur.Inserted {
			result.InsertedCount++
		}
		if ur.FinalityCrossed {
			result.FinalityCrossedCount++
		}
	}

	return result, nil
}
