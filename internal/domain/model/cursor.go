package model

import (
	"time"

	"github.com/google/uuid"
)

type IndexerConfig struct {
	ID               uuid.UUID `db:"id"`
	Chain            Chain     `db:"chain"`
	Network          Network   `db:"network"`
	IsActive         bool      `db:"is_active"`
	TargetBatchSize  int       `db:"target_batch_size"`
	IndexingIntervalMs int    `db:"indexing_interval_ms"`
	ChainConfig      []byte    `db:"chain_config"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
}

type PipelineWatermark struct {
	ID               uuid.UUID `db:"id"`
	Chain            Chain     `db:"chain"`
	Network          Network   `db:"network"`
	IngestedSequence int64     `db:"ingested_sequence"`
	LastHeartbeatAt  time.Time `db:"last_heartbeat_at"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
}
