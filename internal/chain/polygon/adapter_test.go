package polygon

import (
	"log/slog"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/stretchr/testify/assert"
)

func TestAdapter_Chain(t *testing.T) {
	t.Parallel()

	adapter := NewAdapter("https://polygon.example.com", slog.Default())
	assert.Equal(t, "polygon", adapter.Chain())
}

func TestAdapter_ImplementsChainAdapter(t *testing.T) {
	t.Parallel()

	var _ chain.ChainAdapter = NewAdapter("https://polygon.example.com", slog.Default())
}
