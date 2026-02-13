package event

import "github.com/kodax/koda-custody-indexer/internal/domain/model"

// FetchJob represents a unit of work for the fetcher to process.
type FetchJob struct {
	Chain       model.Chain
	Network     model.Network
	Address     string
	CursorValue *string // last processed signature/block
	BatchSize   int
	WalletID    *string
	OrgID       *string
}
