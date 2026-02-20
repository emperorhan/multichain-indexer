package bsc

import (
	"log/slog"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/base"
)

// NewAdapter creates an EVM adapter configured for the BSC (BNB Smart Chain) chain.
func NewAdapter(rpcURL string, logger *slog.Logger, opts ...base.AdapterOption) chain.ChainAdapter {
	return base.NewAdapterWithChain("bsc", rpcURL, logger, opts...)
}
