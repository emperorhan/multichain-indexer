package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

type WatermarkRepo struct {
	db *DB
}

func NewWatermarkRepo(db *DB) *WatermarkRepo {
	return &WatermarkRepo{db: db}
}

func (r *WatermarkRepo) GetWatermark(ctx context.Context, chain model.Chain, network model.Network) (*model.PipelineWatermark, error) {
	var w model.PipelineWatermark
	err := r.db.QueryRowContext(ctx, `
		SELECT id, chain, network, ingested_sequence, last_heartbeat_at, created_at, updated_at
		FROM pipeline_watermarks
		WHERE chain = $1 AND network = $2
	`, chain, network).Scan(
		&w.ID, &w.Chain, &w.Network, &w.IngestedSequence,
		&w.LastHeartbeatAt, &w.CreatedAt, &w.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get watermark: %w", err)
	}
	return &w, nil
}

func (r *WatermarkRepo) UpdateWatermarkTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, ingestedSequence int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO pipeline_watermarks (chain, network, ingested_sequence, last_heartbeat_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (chain, network) DO UPDATE SET
			ingested_sequence = GREATEST(pipeline_watermarks.ingested_sequence, $3),
			last_heartbeat_at = now(),
			updated_at = now()
	`, chain, network, ingestedSequence)
	if err != nil {
		return fmt.Errorf("update watermark: %w", err)
	}
	return nil
}

// RewindWatermarkTx unconditionally sets the watermark to the given sequence,
// bypassing the GREATEST guard. Use this only for intentional rewinds (reorg rollback).
// Uses INSERT ON CONFLICT to handle the case where the watermark row doesn't exist yet.
func (r *WatermarkRepo) RewindWatermarkTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, ingestedSequence int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO pipeline_watermarks (chain, network, ingested_sequence, last_heartbeat_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (chain, network) DO UPDATE SET
			ingested_sequence = $3,
			last_heartbeat_at = now(),
			updated_at = now()
	`, chain, network, ingestedSequence)
	if err != nil {
		return fmt.Errorf("rewind watermark: %w", err)
	}
	return nil
}
