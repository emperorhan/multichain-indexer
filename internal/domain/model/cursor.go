package model

import (
	"time"

	"github.com/google/uuid"
)

type AddressCursor struct {
	ID             uuid.UUID  `db:"id"`
	Chain          Chain      `db:"chain"`
	Network        Network    `db:"network"`
	Address        string     `db:"address"`
	CursorValue    *string    `db:"cursor_value"`
	CursorSequence int64      `db:"cursor_sequence"`
	ItemsProcessed int64      `db:"items_processed"`
	LastFetchedAt  *time.Time `db:"last_fetched_at"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
}

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
	HeadSequence     int64     `db:"head_sequence"`
	IngestedSequence int64     `db:"ingested_sequence"`
	LastHeartbeatAt  time.Time `db:"last_heartbeat_at"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
}
