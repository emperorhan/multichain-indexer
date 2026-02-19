package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

type IndexerConfigRepo struct {
	db *DB
}

func NewIndexerConfigRepo(db *DB) *IndexerConfigRepo {
	return &IndexerConfigRepo{db: db}
}

func (r *IndexerConfigRepo) Get(ctx context.Context, chain model.Chain, network model.Network) (*model.IndexerConfig, error) {
	var c model.IndexerConfig
	err := r.db.QueryRowContext(ctx, `
		SELECT id, chain, network, is_active, target_batch_size, indexing_interval_ms, chain_config, created_at, updated_at
		FROM indexer_configs
		WHERE chain = $1 AND network = $2
	`, chain, network).Scan(
		&c.ID, &c.Chain, &c.Network, &c.IsActive,
		&c.TargetBatchSize, &c.IndexingIntervalMs, &c.ChainConfig,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get indexer config: %w", err)
	}
	return &c, nil
}

func (r *IndexerConfigRepo) Upsert(ctx context.Context, c *model.IndexerConfig) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO indexer_configs (chain, network, is_active, target_batch_size, indexing_interval_ms, chain_config)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (chain, network) DO UPDATE SET
			is_active = EXCLUDED.is_active,
			target_batch_size = EXCLUDED.target_batch_size,
			indexing_interval_ms = EXCLUDED.indexing_interval_ms,
			chain_config = EXCLUDED.chain_config,
			updated_at = now()
	`, c.Chain, c.Network, c.IsActive, c.TargetBatchSize, c.IndexingIntervalMs, c.ChainConfig)
	if err != nil {
		return fmt.Errorf("upsert indexer config: %w", err)
	}
	return nil
}

func (r *IndexerConfigRepo) GetWatermark(ctx context.Context, chain model.Chain, network model.Network) (*model.PipelineWatermark, error) {
	var w model.PipelineWatermark
	err := r.db.QueryRowContext(ctx, `
		SELECT id, chain, network, head_sequence, ingested_sequence, last_heartbeat_at, created_at, updated_at
		FROM pipeline_watermarks
		WHERE chain = $1 AND network = $2
	`, chain, network).Scan(
		&w.ID, &w.Chain, &w.Network, &w.HeadSequence, &w.IngestedSequence,
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

func (r *IndexerConfigRepo) UpdateWatermarkTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, ingestedSequence int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO pipeline_watermarks (chain, network, ingested_sequence, last_heartbeat_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (chain, network) DO UPDATE SET
			ingested_sequence = $3,
			last_heartbeat_at = now(),
			updated_at = now()
	`, chain, network, ingestedSequence)
	if err != nil {
		return fmt.Errorf("update watermark: %w", err)
	}
	return nil
}
