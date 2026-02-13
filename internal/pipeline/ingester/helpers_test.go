package ingester

import (
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

func TestDefaultTokenSymbol(t *testing.T) {
	tests := []struct {
		name     string
		be       event.NormalizedBalanceEvent
		expected string
	}{
		{
			name:     "has symbol",
			be:       event.NormalizedBalanceEvent{TokenSymbol: "USDC"},
			expected: "USDC",
		},
		{
			name:     "native type fallback",
			be:       event.NormalizedBalanceEvent{TokenSymbol: "", TokenType: model.TokenTypeNative},
			expected: "SOL",
		},
		{
			name:     "fungible type fallback",
			be:       event.NormalizedBalanceEvent{TokenSymbol: "", TokenType: model.TokenTypeFungible},
			expected: "UNKNOWN",
		},
		{
			name:     "empty",
			be:       event.NormalizedBalanceEvent{},
			expected: "UNKNOWN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, defaultTokenSymbol(tt.be))
		})
	}
}

func TestDefaultTokenName(t *testing.T) {
	tests := []struct {
		name     string
		be       event.NormalizedBalanceEvent
		expected string
	}{
		{
			name:     "has name",
			be:       event.NormalizedBalanceEvent{TokenName: "USD Coin"},
			expected: "USD Coin",
		},
		{
			name:     "native type fallback",
			be:       event.NormalizedBalanceEvent{TokenName: "", TokenType: model.TokenTypeNative},
			expected: "Solana",
		},
		{
			name:     "fungible type fallback",
			be:       event.NormalizedBalanceEvent{TokenName: "", TokenType: model.TokenTypeFungible},
			expected: "Unknown Token",
		},
		{
			name:     "empty",
			be:       event.NormalizedBalanceEvent{},
			expected: "Unknown Token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, defaultTokenName(tt.be))
		})
	}
}
