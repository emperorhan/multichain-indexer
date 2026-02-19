package event

import (
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// ReorgEvent signals that a block reorganization was detected.
// ForkBlockNumber is the first block where the on-chain hash diverges from the indexed hash.
type ReorgEvent struct {
	Chain           model.Chain
	Network         model.Network
	ForkBlockNumber int64
	ExpectedHash    string // hash stored in DB
	ActualHash      string // hash observed on-chain
	DetectedAt      time.Time
}
