package event

import "github.com/emperorhan/multichain-indexer/internal/domain/model"

// FinalityPromotion signals that the chain's finalized block number has advanced.
// The ingester should promote all indexed blocks up to NewFinalizedBlock.
type FinalityPromotion struct {
	Chain             model.Chain
	Network           model.Network
	NewFinalizedBlock int64
}
