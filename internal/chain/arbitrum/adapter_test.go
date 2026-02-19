package arbitrum

import (
	"log/slog"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/stretchr/testify/assert"
)

func TestAdapter_Chain(t *testing.T) {
	t.Parallel()

	adapter := NewAdapter("https://arbitrum.example.com", slog.Default())
	assert.Equal(t, "arbitrum", adapter.Chain())
}

func TestAdapter_ImplementsChainAdapter(t *testing.T) {
	t.Parallel()

	var _ chain.ChainAdapter = NewAdapter("https://arbitrum.example.com", slog.Default())
}
