package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

const (
	// colsPerEvent is the number of columns in the VALUES clause for balance_events.
	colsPerEvent = 33
	// bulkChunkSize is the maximum number of events per multi-VALUES INSERT.
	// PostgreSQL supports up to 65535 parameters; 33 cols × 1500 = 49500 < 65535.
	bulkChunkSize = 1500
	// smallBatchThreshold: batches at or below this size use the per-event path
	// which provides finality crossing detection.
	smallBatchThreshold = 5
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
			finality_rank(EXCLUDED.finality_state) >= finality_rank(balance_events.finality_state)
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

// BulkUpsertTx upserts multiple balance events.
// For small batches (≤5), it delegates to the per-event UpsertTx path which
// provides finality crossing detection. For larger batches, it uses a
// multi-VALUES INSERT...ON CONFLICT for dramatically fewer round trips.
func (r *BalanceEventRepo) BulkUpsertTx(ctx context.Context, tx *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
	if len(events) == 0 {
		return store.BulkUpsertEventResult{}, nil
	}

	// Small batch: per-event path for finality crossing detection
	if len(events) <= smallBatchThreshold {
		return r.bulkUpsertPerEvent(ctx, tx, events)
	}

	// Large batch: multi-VALUES bulk insert
	return r.bulkUpsertMultiValues(ctx, tx, events)
}

// bulkUpsertPerEvent upserts a small batch of events using a single CTE query
// that provides finality crossing detection. Reduces 2×N queries to 1.
func (r *BalanceEventRepo) bulkUpsertPerEvent(ctx context.Context, tx *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
	var result store.BulkUpsertEventResult
	if len(events) == 0 {
		return result, nil
	}

	// Collect event_ids for the existing-state lookup.
	eventIDs := make([]interface{}, len(events))
	for i, ev := range events {
		eventIDs[i] = ev.EventID
	}

	// Build the existing-state lookup placeholders: $1, $2, ..., $N
	existingPlaceholders := make([]string, len(events))
	for i := range events {
		existingPlaceholders[i] = fmt.Sprintf("$%d", i+1)
	}

	// Build the CTE query.
	var sb strings.Builder
	sb.WriteString(`WITH existing AS (
		SELECT event_id, balance_applied FROM balance_events
		WHERE event_id IN (`)
	sb.WriteString(strings.Join(existingPlaceholders, ", "))
	sb.WriteString(`) FOR UPDATE
	),
	upserted AS (
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
		) VALUES `)

	// Build VALUES rows. Parameters start after the N event_id params.
	args := make([]interface{}, 0, len(events)+len(events)*colsPerEvent)
	args = append(args, eventIDs...)

	for idx, be := range events {
		if idx > 0 {
			sb.WriteString(", ")
		}
		base := len(events) + idx*colsPerEvent
		sb.WriteString("(")
		for j := 0; j < colsPerEvent; j++ {
			if j > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "$%d", base+j+1)
		}
		sb.WriteString(")")

		args = append(args,
			be.Chain, be.Network, be.TransactionID, be.TxHash,
			be.OuterInstructionIndex, be.InnerInstructionIndex,
			be.TokenID, be.ActivityType, be.EventAction, be.ProgramID,
			be.Address, be.CounterpartyAddress, be.Delta, be.BalanceBefore, be.BalanceAfter,
			be.WatchedAddress, be.WalletID, be.OrganizationID,
			be.BlockCursor, be.BlockTime, be.ChainData, be.BalanceApplied,
			be.EventID, be.BlockHash, be.TxIndex, be.EventPath, be.EventPathType,
			be.ActorAddress, be.AssetType, be.AssetID,
			be.FinalityState, be.DecoderVersion, be.SchemaVersion,
		)
	}

	sb.WriteString(`
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
			finality_rank(EXCLUDED.finality_state) >= finality_rank(balance_events.finality_state)
		RETURNING event_id, (xmax = 0) AS inserted, balance_applied
	)
	SELECT u.event_id, u.inserted,
	       COALESCE(e.balance_applied, false) AS was_applied,
	       u.balance_applied AS now_applied
	FROM upserted u LEFT JOIN existing e ON u.event_id = e.event_id`)

	rows, err := tx.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return result, fmt.Errorf("bulk upsert per-event CTE: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var eventID string
		var inserted, wasApplied, nowApplied bool
		if err := rows.Scan(&eventID, &inserted, &wasApplied, &nowApplied); err != nil {
			return result, fmt.Errorf("bulk upsert per-event CTE scan: %w", err)
		}
		if inserted {
			result.InsertedCount++
		}
		// Finality crossing: existing row was not applied, now it is, and it was an update (not insert).
		if !inserted && !wasApplied && nowApplied {
			result.FinalityCrossedCount++
		}
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("bulk upsert per-event CTE rows: %w", err)
	}

	return result, nil
}

// bulkUpsertMultiValues uses a multi-VALUES INSERT...ON CONFLICT statement,
// processing events in chunks of bulkChunkSize to stay within PostgreSQL's
// 65535 parameter limit.
func (r *BalanceEventRepo) bulkUpsertMultiValues(ctx context.Context, tx *sql.Tx, events []*model.BalanceEvent) (store.BulkUpsertEventResult, error) {
	var result store.BulkUpsertEventResult

	if len(events) > 0 {
		chain := string(events[0].Chain)
		network := string(events[0].Network)
		metrics.BalanceEventBulkInsertSize.WithLabelValues(chain, network).Observe(float64(len(events)))
	}

	for i := 0; i < len(events); i += bulkChunkSize {
		end := i + bulkChunkSize
		if end > len(events) {
			end = len(events)
		}
		chunk := events[i:end]

		inserted, err := r.execBulkChunk(ctx, tx, chunk)
		if err != nil {
			return result, err
		}
		result.InsertedCount += inserted
	}

	return result, nil
}

// execBulkChunk executes a single multi-VALUES INSERT...ON CONFLICT for a chunk of events.
func (r *BalanceEventRepo) execBulkChunk(ctx context.Context, tx *sql.Tx, chunk []*model.BalanceEvent) (int, error) {
	// Build parameterized VALUES clause
	var sb strings.Builder
	sb.WriteString(`
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
		) VALUES `)

	args := make([]interface{}, 0, len(chunk)*colsPerEvent)
	for idx, be := range chunk {
		if idx > 0 {
			sb.WriteString(", ")
		}
		base := idx * colsPerEvent
		sb.WriteString("(")
		for j := 0; j < colsPerEvent; j++ {
			if j > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "$%d", base+j+1)
		}
		sb.WriteString(")")

		args = append(args,
			be.Chain,                 // $1
			be.Network,               // $2
			be.TransactionID,         // $3
			be.TxHash,                // $4
			be.OuterInstructionIndex, // $5
			be.InnerInstructionIndex, // $6
			be.TokenID,               // $7
			be.ActivityType,          // $8
			be.EventAction,           // $9
			be.ProgramID,             // $10
			be.Address,               // $11
			be.CounterpartyAddress,   // $12
			be.Delta,                 // $13
			be.BalanceBefore,         // $14
			be.BalanceAfter,          // $15
			be.WatchedAddress,        // $16
			be.WalletID,              // $17
			be.OrganizationID,        // $18
			be.BlockCursor,           // $19
			be.BlockTime,             // $20
			be.ChainData,             // $21
			be.BalanceApplied,        // $22
			be.EventID,               // $23
			be.BlockHash,             // $24
			be.TxIndex,               // $25
			be.EventPath,             // $26
			be.EventPathType,         // $27
			be.ActorAddress,          // $28
			be.AssetType,             // $29
			be.AssetID,               // $30
			be.FinalityState,         // $31
			be.DecoderVersion,        // $32
			be.SchemaVersion,         // $33
		)
	}

	sb.WriteString(`
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
			finality_rank(EXCLUDED.finality_state) >= finality_rank(balance_events.finality_state)
		RETURNING (xmax = 0) AS inserted
	`)

	rows, err := tx.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return 0, fmt.Errorf("bulk upsert balance events: %w", err)
	}
	defer rows.Close()

	insertedCount := 0
	for rows.Next() {
		var inserted bool
		if err := rows.Scan(&inserted); err != nil {
			return 0, fmt.Errorf("bulk upsert scan: %w", err)
		}
		if inserted {
			insertedCount++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("bulk upsert rows: %w", err)
	}

	return insertedCount, nil
}

