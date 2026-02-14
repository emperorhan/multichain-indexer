package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizedBalanceEventSerialization_Fixtures(t *testing.T) {
	normalizeRawMessage := func(in json.RawMessage) json.RawMessage {
		if in == nil {
			return json.RawMessage("null")
		}
		return in
	}

	cases := []struct {
		name    string
		fixture string
	}{
		{
			name:    "solana",
			fixture: filepath.Join("testdata", "solana_canonical_envelope.json"),
		},
		{
			name:    "base",
			fixture: filepath.Join("testdata", "base_canonical_envelope.json"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(tc.fixture)
			require.NoError(t, err)

			var parsed NormalizedBalanceEvent
			require.NoError(t, json.Unmarshal(raw, &parsed))
			serialized, err := json.Marshal(parsed)
			require.NoError(t, err)

			var roundTrip NormalizedBalanceEvent
			require.NoError(t, json.Unmarshal(serialized, &roundTrip))
			parsed.ChainData = normalizeRawMessage(parsed.ChainData)
			roundTrip.ChainData = normalizeRawMessage(roundTrip.ChainData)
			assert.Equal(t, roundTrip, parsed)
			assert.NotEmpty(t, parsed.EventID)
			assert.NotEmpty(t, parsed.EventPath)
		})
	}
}
