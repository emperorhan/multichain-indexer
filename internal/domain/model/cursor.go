package model

import (
	"time"

	"github.com/google/uuid"
)

type PipelineWatermark struct {
	ID               uuid.UUID `db:"id"`
	Chain            Chain     `db:"chain"`
	Network          Network   `db:"network"`
	IngestedSequence int64     `db:"ingested_sequence"`
	LastHeartbeatAt  time.Time `db:"last_heartbeat_at"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
}
