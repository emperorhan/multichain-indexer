package event

import "github.com/emperorhan/multichain-indexer/internal/domain/model"

// FetchJob represents a unit of work for the fetcher to process.
type FetchJob struct {
	Chain          model.Chain
	Network        model.Network
	Address        string
	CursorValue    *string // last processed signature/block
	CursorSequence int64
	FetchCutoffSeq int64 // pinned per-tick upper sequence bound (inclusive)
	BatchSize      int
	WalletID       *string
	OrgID          *string

	// BlockScanMode fields — used for block-based chains (EVM, BTC)
	// where a single scan covers all watched addresses.
	BlockScanMode    bool
	StartBlock       int64
	EndBlock         int64
	WatchedAddresses []string
}
