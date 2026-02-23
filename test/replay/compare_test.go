package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// HasMismatch
// ---------------------------------------------------------------------------

func TestHasMismatch_AllEmpty(t *testing.T) {
	r := CompareResult{}
	assert.False(t, r.HasMismatch())
}

func TestHasMismatch_Missing(t *testing.T) {
	r := CompareResult{Missing: []string{"evt-1"}}
	assert.True(t, r.HasMismatch())
}

func TestHasMismatch_Extra(t *testing.T) {
	r := CompareResult{Extra: []string{"evt-2"}}
	assert.True(t, r.HasMismatch())
}

func TestHasMismatch_Divergent(t *testing.T) {
	r := CompareResult{Divergent: []DivergentEvent{{EventID: "evt-3", Field: "delta"}}}
	assert.True(t, r.HasMismatch())
}

func TestHasMismatch_MatchingOnly(t *testing.T) {
	r := CompareResult{Matching: []string{"evt-1", "evt-2"}}
	assert.False(t, r.HasMismatch())
}

// ---------------------------------------------------------------------------
// compareEvents
// ---------------------------------------------------------------------------

func TestCompareEvents_PerfectMatch(t *testing.T) {
	replay := []event.NormalizedTransaction{
		{
			TxHash: "0xabc",
			BalanceEvents: []event.NormalizedBalanceEvent{
				{
					EventID:             "evt-1",
					ActivityType:        model.ActivityDeposit,
					Delta:               "1000",
					ActorAddress:        "0xActor",
					AssetID:             "ETH",
					CounterpartyAddress: "0xCounter",
					ContractAddress:     "0xContract",
				},
				{
					EventID:             "evt-2",
					ActivityType:        model.ActivityFee,
					Delta:               "-50",
					ActorAddress:        "0xActor",
					AssetID:             "ETH",
					CounterpartyAddress: "",
					ContractAddress:     "0xContract",
				},
			},
		},
	}
	dbEvents := []model.BalanceEvent{
		{
			EventID:             "evt-1",
			ActivityType:        model.ActivityDeposit,
			Delta:               "1000",
			ActorAddress:        "0xActor",
			AssetID:             "ETH",
			CounterpartyAddress: "0xCounter",
			ProgramID:           "0xContract",
		},
		{
			EventID:             "evt-2",
			ActivityType:        model.ActivityFee,
			Delta:               "-50",
			ActorAddress:        "0xActor",
			AssetID:             "ETH",
			CounterpartyAddress: "",
			ProgramID:           "0xContract",
		},
	}

	result := compareEvents(replay, dbEvents)

	assert.False(t, result.HasMismatch())
	assert.Len(t, result.Matching, 2)
	assert.Empty(t, result.Missing)
	assert.Empty(t, result.Extra)
	assert.Empty(t, result.Divergent)
}

func TestCompareEvents_MissingEvents(t *testing.T) {
	replay := []event.NormalizedTransaction{
		{
			BalanceEvents: []event.NormalizedBalanceEvent{
				{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "100"},
				{EventID: "evt-2", ActivityType: model.ActivityDeposit, Delta: "200"},
			},
		},
	}
	// DB only has evt-1.
	dbEvents := []model.BalanceEvent{
		{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "100"},
	}

	result := compareEvents(replay, dbEvents)

	assert.True(t, result.HasMismatch())
	assert.Len(t, result.Matching, 1)
	assert.Equal(t, []string{"evt-2"}, result.Missing)
	assert.Empty(t, result.Extra)
}

func TestCompareEvents_ExtraEvents(t *testing.T) {
	replay := []event.NormalizedTransaction{
		{
			BalanceEvents: []event.NormalizedBalanceEvent{
				{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "100"},
			},
		},
	}
	// DB has evt-1 and evt-3 (extra).
	dbEvents := []model.BalanceEvent{
		{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "100"},
		{EventID: "evt-3", ActivityType: model.ActivityWithdrawal, Delta: "-500"},
	}

	result := compareEvents(replay, dbEvents)

	assert.True(t, result.HasMismatch())
	assert.Len(t, result.Matching, 1)
	assert.Empty(t, result.Missing)
	assert.Equal(t, []string{"evt-3"}, result.Extra)
}

func TestCompareEvents_DivergentFields(t *testing.T) {
	replay := []event.NormalizedTransaction{
		{
			BalanceEvents: []event.NormalizedBalanceEvent{
				{
					EventID:      "evt-1",
					ActivityType: model.ActivityDeposit,
					Delta:        "1000",
					ActorAddress: "0xActorReplay",
					AssetID:      "ETH",
				},
			},
		},
	}
	dbEvents := []model.BalanceEvent{
		{
			EventID:      "evt-1",
			ActivityType: model.ActivityWithdrawal, // different
			Delta:        "999",                    // different
			ActorAddress: "0xActorDB",              // different
			AssetID:      "ETH",
		},
	}

	result := compareEvents(replay, dbEvents)

	assert.True(t, result.HasMismatch())
	assert.Empty(t, result.Matching)
	assert.Empty(t, result.Missing)
	assert.Empty(t, result.Extra)
	assert.Len(t, result.Divergent, 3)

	// Sorted by event_id then field.
	assert.Equal(t, "activity_type", result.Divergent[0].Field)
	assert.Equal(t, "DEPOSIT", result.Divergent[0].ReplayValue)
	assert.Equal(t, "WITHDRAWAL", result.Divergent[0].DBValue)

	assert.Equal(t, "actor_address", result.Divergent[1].Field)
	assert.Equal(t, "delta", result.Divergent[2].Field)
}

func TestCompareEvents_EmptyBothSides(t *testing.T) {
	result := compareEvents(nil, nil)

	assert.False(t, result.HasMismatch())
	assert.Empty(t, result.Matching)
	assert.Empty(t, result.Missing)
	assert.Empty(t, result.Extra)
	assert.Empty(t, result.Divergent)
}

func TestCompareEvents_EmptyEventID_Skipped(t *testing.T) {
	replay := []event.NormalizedTransaction{
		{
			BalanceEvents: []event.NormalizedBalanceEvent{
				{EventID: "", ActivityType: model.ActivityDeposit, Delta: "100"},
				{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "200"},
			},
		},
	}
	dbEvents := []model.BalanceEvent{
		{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "200"},
	}

	result := compareEvents(replay, dbEvents)

	assert.False(t, result.HasMismatch())
	assert.Len(t, result.Matching, 1)
}

func TestCompareEvents_MultiTx(t *testing.T) {
	replay := []event.NormalizedTransaction{
		{
			TxHash: "tx1",
			BalanceEvents: []event.NormalizedBalanceEvent{
				{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "100"},
			},
		},
		{
			TxHash: "tx2",
			BalanceEvents: []event.NormalizedBalanceEvent{
				{EventID: "evt-2", ActivityType: model.ActivityWithdrawal, Delta: "-50"},
			},
		},
	}
	dbEvents := []model.BalanceEvent{
		{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "100"},
		{EventID: "evt-2", ActivityType: model.ActivityWithdrawal, Delta: "-50"},
	}

	result := compareEvents(replay, dbEvents)

	assert.False(t, result.HasMismatch())
	assert.Len(t, result.Matching, 2)
}

func TestCompareEvents_MixedMissingExtraDivergent(t *testing.T) {
	replay := []event.NormalizedTransaction{
		{
			BalanceEvents: []event.NormalizedBalanceEvent{
				{EventID: "match", ActivityType: model.ActivityDeposit, Delta: "100"},
				{EventID: "missing", ActivityType: model.ActivityDeposit, Delta: "200"},
				{EventID: "diverged", ActivityType: model.ActivityDeposit, Delta: "999"},
			},
		},
	}
	dbEvents := []model.BalanceEvent{
		{EventID: "match", ActivityType: model.ActivityDeposit, Delta: "100"},
		{EventID: "extra", ActivityType: model.ActivityFee, Delta: "-10"},
		{EventID: "diverged", ActivityType: model.ActivityDeposit, Delta: "111"},
	}

	result := compareEvents(replay, dbEvents)

	assert.True(t, result.HasMismatch())
	assert.Equal(t, []string{"match"}, result.Matching)
	assert.Equal(t, []string{"missing"}, result.Missing)
	assert.Equal(t, []string{"extra"}, result.Extra)
	require.Len(t, result.Divergent, 1)
	assert.Equal(t, "delta", result.Divergent[0].Field)
	assert.Equal(t, "999", result.Divergent[0].ReplayValue)
	assert.Equal(t, "111", result.Divergent[0].DBValue)
}

func TestCompareEvents_DeterministicOrder(t *testing.T) {
	replay := []event.NormalizedTransaction{
		{
			BalanceEvents: []event.NormalizedBalanceEvent{
				{EventID: "zzz", ActivityType: model.ActivityDeposit, Delta: "1"},
				{EventID: "aaa", ActivityType: model.ActivityDeposit, Delta: "2"},
				{EventID: "mmm", ActivityType: model.ActivityDeposit, Delta: "3"},
			},
		},
	}
	dbEvents := []model.BalanceEvent{
		{EventID: "zzz", ActivityType: model.ActivityDeposit, Delta: "1"},
		{EventID: "aaa", ActivityType: model.ActivityDeposit, Delta: "2"},
		{EventID: "mmm", ActivityType: model.ActivityDeposit, Delta: "3"},
	}

	result := compareEvents(replay, dbEvents)

	assert.Equal(t, []string{"aaa", "mmm", "zzz"}, result.Matching)
}

func TestCompareEvents_CounterpartyDivergence(t *testing.T) {
	replay := []event.NormalizedTransaction{
		{
			BalanceEvents: []event.NormalizedBalanceEvent{
				{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "100",
					CounterpartyAddress: "0xSender"},
			},
		},
	}
	dbEvents := []model.BalanceEvent{
		{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "100",
			CounterpartyAddress: "0xDifferentSender"},
	}

	result := compareEvents(replay, dbEvents)

	assert.True(t, result.HasMismatch())
	require.Len(t, result.Divergent, 1)
	assert.Equal(t, "counterparty_address", result.Divergent[0].Field)
}

func TestCompareEvents_ContractAddressMapsFromProgramID(t *testing.T) {
	// Replay uses ContractAddress, DB maps from ProgramID.
	replay := []event.NormalizedTransaction{
		{
			BalanceEvents: []event.NormalizedBalanceEvent{
				{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "100",
					ContractAddress: "0xToken"},
			},
		},
	}
	dbEvents := []model.BalanceEvent{
		{EventID: "evt-1", ActivityType: model.ActivityDeposit, Delta: "100",
			ProgramID: "0xToken"},
	}

	result := compareEvents(replay, dbEvents)

	assert.False(t, result.HasMismatch())
	assert.Len(t, result.Matching, 1)
}

// ---------------------------------------------------------------------------
// printTextReport
// ---------------------------------------------------------------------------

func TestPrintTextReport_Match(t *testing.T) {
	result := CompareResult{Matching: []string{"evt-1", "evt-2"}}
	var buf bytes.Buffer
	printTextReport(&buf, "ethereum", "mainnet", 100, 110, 2, 2, result)
	out := buf.String()

	assert.Contains(t, out, "=== Replay Verification Report ===")
	assert.Contains(t, out, "Chain: ethereum / mainnet")
	assert.Contains(t, out, "Block range: 100 - 110")
	assert.Contains(t, out, "Replay events: 2")
	assert.Contains(t, out, "DB events: 2")
	assert.Contains(t, out, "Matching: 2")
	assert.Contains(t, out, "Missing: 0")
	assert.Contains(t, out, "Extra: 0")
	assert.Contains(t, out, "Result: MATCH")
	assert.NotContains(t, out, "MISMATCH")
}

func TestPrintTextReport_Mismatch(t *testing.T) {
	result := CompareResult{
		Matching:  []string{"evt-1"},
		Missing:   []string{"evt-2"},
		Extra:     []string{"evt-3"},
		Divergent: []DivergentEvent{{EventID: "evt-4", Field: "delta", ReplayValue: "100", DBValue: "200"}},
	}
	var buf bytes.Buffer
	printTextReport(&buf, "solana", "devnet", 300000000, 300000010, 3, 3, result)
	out := buf.String()

	assert.Contains(t, out, "Result: MISMATCH")
	assert.Contains(t, out, "--- Missing (in replay but not in DB) ---")
	assert.Contains(t, out, "evt-2")
	assert.Contains(t, out, "--- Extra (in DB but not in replay) ---")
	assert.Contains(t, out, "evt-3")
	assert.Contains(t, out, "--- Divergent (field mismatches) ---")
	assert.Contains(t, out, "evt-4: delta")
}

func TestPrintTextReport_EmptyResult(t *testing.T) {
	result := CompareResult{}
	var buf bytes.Buffer
	printTextReport(&buf, "btc", "mainnet", 800000, 800010, 0, 0, result)
	out := buf.String()

	assert.Contains(t, out, "Replay events: 0")
	assert.Contains(t, out, "DB events: 0")
	assert.Contains(t, out, "Result: MATCH")
}

// ---------------------------------------------------------------------------
// printJSONReport
// ---------------------------------------------------------------------------

func TestPrintJSONReport_Match(t *testing.T) {
	result := CompareResult{Matching: []string{"evt-1"}}
	var buf bytes.Buffer
	err := printJSONReport(&buf, "ethereum", "mainnet", 100, 110, 1, 1, result)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))

	assert.Equal(t, "ethereum", parsed["chain"])
	assert.Equal(t, "mainnet", parsed["network"])
	assert.Equal(t, float64(100), parsed["start_block"])
	assert.Equal(t, float64(110), parsed["end_block"])
	assert.Equal(t, float64(1), parsed["replay_events"])
	assert.Equal(t, float64(1), parsed["db_events"])
	assert.Equal(t, "MATCH", parsed["result"])
}

func TestPrintJSONReport_Mismatch(t *testing.T) {
	result := CompareResult{Missing: []string{"evt-1"}}
	var buf bytes.Buffer
	err := printJSONReport(&buf, "solana", "devnet", 1, 10, 1, 0, result)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))

	assert.Equal(t, "MISMATCH", parsed["result"])
	compare := parsed["compare"].(map[string]any)
	missing := compare["missing"].([]any)
	assert.Len(t, missing, 1)
	assert.Equal(t, "evt-1", missing[0])
}

func TestPrintJSONReport_ValidJSON(t *testing.T) {
	result := CompareResult{
		Matching:  []string{"a", "b"},
		Missing:   []string{"c"},
		Extra:     []string{"d"},
		Divergent: []DivergentEvent{{EventID: "e", Field: "delta", ReplayValue: "1", DBValue: "2"}},
	}
	var buf bytes.Buffer
	err := printJSONReport(&buf, "base", "mainnet", 19999800, 19999810, 3, 3, result)
	require.NoError(t, err)

	// Verify valid JSON.
	assert.True(t, json.Valid(buf.Bytes()), "output should be valid JSON")

	// Verify roundtrip.
	var parsed struct {
		Compare struct {
			Matching  []string        `json:"matching"`
			Missing   []string        `json:"missing"`
			Extra     []string        `json:"extra"`
			Divergent []DivergentEvent `json:"divergent"`
		} `json:"compare"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))
	assert.Equal(t, []string{"a", "b"}, parsed.Compare.Matching)
	assert.Equal(t, []string{"c"}, parsed.Compare.Missing)
	assert.Equal(t, []string{"d"}, parsed.Compare.Extra)
	require.Len(t, parsed.Compare.Divergent, 1)
	assert.Equal(t, "e", parsed.Compare.Divergent[0].EventID)
}

func TestPrintJSONReport_IndentedOutput(t *testing.T) {
	result := CompareResult{}
	var buf bytes.Buffer
	err := printJSONReport(&buf, "eth", "mainnet", 1, 2, 0, 0, result)
	require.NoError(t, err)

	// Indented JSON should contain newlines + spaces.
	assert.True(t, strings.Contains(buf.String(), "\n  "))
}

// ---------------------------------------------------------------------------
// compareEvents: all 6 fields divergent at once
// ---------------------------------------------------------------------------

func TestCompareEvents_AllFieldsDivergent(t *testing.T) {
	replay := []event.NormalizedTransaction{
		{
			BalanceEvents: []event.NormalizedBalanceEvent{
				{
					EventID:             "evt-1",
					ActivityType:        model.ActivityDeposit,
					Delta:               "100",
					ActorAddress:        "actor-r",
					AssetID:             "asset-r",
					CounterpartyAddress: "counter-r",
					ContractAddress:     "contract-r",
				},
			},
		},
	}
	dbEvents := []model.BalanceEvent{
		{
			EventID:             "evt-1",
			ActivityType:        model.ActivityWithdrawal,
			Delta:               "999",
			ActorAddress:        "actor-d",
			AssetID:             "asset-d",
			CounterpartyAddress: "counter-d",
			ProgramID:           "contract-d",
		},
	}

	result := compareEvents(replay, dbEvents)

	assert.True(t, result.HasMismatch())
	assert.Empty(t, result.Matching)
	assert.Len(t, result.Divergent, 6)

	fields := make(map[string]bool)
	for _, d := range result.Divergent {
		fields[d.Field] = true
	}
	assert.True(t, fields["activity_type"])
	assert.True(t, fields["delta"])
	assert.True(t, fields["actor_address"])
	assert.True(t, fields["asset_id"])
	assert.True(t, fields["counterparty_address"])
	assert.True(t, fields["contract_address"])
}

// ---------------------------------------------------------------------------
// compareEvents: duplicate event_id in replay (last-writer-wins via map)
// ---------------------------------------------------------------------------

func TestCompareEvents_DuplicateEventID_LastWins(t *testing.T) {
	replay := []event.NormalizedTransaction{
		{
			BalanceEvents: []event.NormalizedBalanceEvent{
				{EventID: "dup", ActivityType: model.ActivityDeposit, Delta: "first"},
				{EventID: "dup", ActivityType: model.ActivityDeposit, Delta: "second"},
			},
		},
	}
	dbEvents := []model.BalanceEvent{
		{EventID: "dup", ActivityType: model.ActivityDeposit, Delta: "second"},
	}

	result := compareEvents(replay, dbEvents)

	// Last replay entry for "dup" has delta="second", which matches DB.
	assert.False(t, result.HasMismatch())
	assert.Len(t, result.Matching, 1)
}
