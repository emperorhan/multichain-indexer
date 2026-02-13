package ingester

import (
	"testing"

	"github.com/kodax/koda-custody-indexer/internal/domain/event"
	"github.com/kodax/koda-custody-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

func TestNegateAmount(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"positive to negative", "1000", "-1000"},
		{"negative to positive", "-500", "500"},
		{"zero stays zero", "0", "0"},
		{"large number", "999999999999999999", "-999999999999999999"},
		{"invalid input returns 0", "invalid", "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := negateAmount(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultTokenSymbol(t *testing.T) {
	tests := []struct {
		name     string
		nt       event.NormalizedTransfer
		expected string
	}{
		{
			name:     "has symbol",
			nt:       event.NormalizedTransfer{TokenSymbol: "USDC"},
			expected: "USDC",
		},
		{
			name:     "native type fallback",
			nt:       event.NormalizedTransfer{TokenSymbol: "", TokenType: model.TokenTypeNative},
			expected: "SOL",
		},
		{
			name:     "fungible type fallback",
			nt:       event.NormalizedTransfer{TokenSymbol: "", TokenType: model.TokenTypeFungible},
			expected: "UNKNOWN",
		},
		{
			name:     "empty",
			nt:       event.NormalizedTransfer{},
			expected: "UNKNOWN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, defaultTokenSymbol(tt.nt))
		})
	}
}

func TestDefaultTokenName(t *testing.T) {
	tests := []struct {
		name     string
		nt       event.NormalizedTransfer
		expected string
	}{
		{
			name:     "has name",
			nt:       event.NormalizedTransfer{TokenName: "USD Coin"},
			expected: "USD Coin",
		},
		{
			name:     "native type fallback",
			nt:       event.NormalizedTransfer{TokenName: "", TokenType: model.TokenTypeNative},
			expected: "Solana",
		},
		{
			name:     "fungible type fallback",
			nt:       event.NormalizedTransfer{TokenName: "", TokenType: model.TokenTypeFungible},
			expected: "Unknown Token",
		},
		{
			name:     "empty",
			nt:       event.NormalizedTransfer{},
			expected: "Unknown Token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, defaultTokenName(tt.nt))
		})
	}
}
