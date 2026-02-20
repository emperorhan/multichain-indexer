package ethereum

import (
	"log/slog"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/base"
)

// NewAdapter creates an EVM adapter configured for the Ethereum chain.
func NewAdapter(rpcURL string, logger *slog.Logger, opts ...base.AdapterOption) chain.ChainAdapter {
	return base.NewAdapterWithChain("ethereum", rpcURL, logger, opts...)
}
