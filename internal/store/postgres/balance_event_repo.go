package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

type BalanceEventRepo struct {
	db *DB
}

func NewBalanceEventRepo(db *DB) *BalanceEventRepo {
	return &BalanceEventRepo{db: db}
}

// UpsertTx performs a 2-query upsert for a balance event using event_id as
// the dedup key (flat table, no canonical_ids indirection).
//
// Query 1: SELECT balance_applied FROM balance_events WHERE event_id = $1 FOR UPDATE
// Query 2: INSERT ... ON CONFLICT (event_id) DO UPDATE SET ... (finality hierarchy)
func (r *BalanceEventRepo) UpsertTx(ctx context.Context, tx *sql.Tx, be *model.BalanceEvent) (store.UpsertResult, error) {
	if be.EventID == "" {
		return store.UpsertResult{}, fmt.Errorf("upsert balance event: event_id is required")
	}

	// Query 1: Lock existing row (if any) and read balance_applied state.
	var existingBalanceApplied bool
	var existingRowFound bool
	err := tx.QueryRowContext(ctx, `
		SELECT balance_applied FROM balance_events
		WHERE event_id = $1
		FOR UPDATE
	`, be.EventID).Scan(&existingBalanceApplied)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return store.UpsertResult{}, fmt.Errorf("upsert balance event: lock existing: %w", err)
	}
	existingRowFound = err == nil

	// Query 2: INSERT ... ON CONFLICT (event_id) DO UPDATE with finality hierarchy.
	const sqlUpsert = `
		INSERT INTO balance_events (
			chain, network, transaction_id, tx_hash,
			outer_instruction_index, inner_instruction_index,
			token_id, activity_type, event_action, program_id,
			address, counterparty_address, delta, balance_before, balance_after,
			watched_address, wallet_id, organization_id,
			block_cursor, block_time, chain_data, balance_applied,
			event_id, block_hash, tx_index, event_path, event_path_type,
			actor_address, asset_type, asset_id,
			finality_state, decoder_version, schema_version
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
			$16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33
		)
		ON CONFLICT (event_id) WHERE event_id <> '' DO UPDATE SET
			finality_state = EXCLUDED.finality_state,
			balance_applied = CASE
				WHEN EXCLUDED.balance_applied AND NOT balance_events.balance_applied THEN true
				ELSE balance_events.balance_applied
			END,
			block_hash = COALESCE(NULLIF(EXCLUDED.block_hash, ''), balance_events.block_hash),
			tx_index = CASE
				WHEN EXCLUDED.tx_index <> 0 THEN EXCLUDED.tx_index
				ELSE balance_events.tx_index
			END,
			block_cursor = GREATEST(balance_events.block_cursor, EXCLUDED.block_cursor),
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
		RETURNING (xmax = 0) AS inserted
	`

	var inserted bool
	err = tx.QueryRowContext(
		ctx,
		sqlUpsert,
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
			// ON CONFLICT matched but WHERE clause rejected the update (lower finality).
			return store.UpsertResult{}, nil
		}
		return store.UpsertResult{}, fmt.Errorf("upsert balance event: %w", err)
	}

	if inserted {
		return store.UpsertResult{Inserted: true}, nil
	}

	// Updated existing row — check if finality crossed.
	finalityCrossed := existingRowFound && !existingBalanceApplied && be.BalanceApplied
	return store.UpsertResult{FinalityCrossed: finalityCrossed}, nil
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
