package polygon

import (
	"log/slog"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/base"
)

// NewAdapter creates an EVM adapter configured for the Polygon chain.
func NewAdapter(rpcURL string, logger *slog.Logger) chain.ChainAdapter {
	return base.NewAdapterWithChain("polygon", rpcURL, logger)
}
