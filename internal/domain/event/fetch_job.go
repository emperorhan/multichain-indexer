package event

import "github.com/emperorhan/multichain-indexer/internal/domain/model"

// FetchJob represents a unit of work for the fetcher to process.
type FetchJob struct {
	Chain          model.Chain
	Network        model.Network
	Address        string
	CursorValue    *string // last processed signature/block
	CursorSequence int64
	BatchSize      int
	WalletID       *string
	OrgID          *string
}
