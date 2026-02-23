package ingester

import (
	"encoding/json"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Allocation regression tests: guard hot-path functions against alloc creep.
// Uses testing.AllocsPerRun to enforce upper bounds.
// ---------------------------------------------------------------------------

func TestAllocRegression_BuildReversalAdjustItems_SingleDeposit(t *testing.T) {
	tokenID := uuid.New()
	events := []rollbackBalanceEvent{
		{TokenID: tokenID, Address: "addr1", Delta: "1000", BlockCursor: 100, TxHash: "tx1", ActivityType: model.ActivityDeposit, BalanceApplied: true},
	}
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = buildReversalAdjustItems(events)
	})
	// Single deposit: 1 slice alloc + 1 big.Int parse + 1 big.Int negate → allow up to 10
	assert.LessOrEqual(t, allocs, float64(10), "buildReversalAdjustItems allocs should stay bounded")
}

func TestAllocRegression_BuildReversalAdjustItems_FiveEvents(t *testing.T) {
	events := make([]rollbackBalanceEvent, 5)
	for i := range events {
		events[i] = rollbackBalanceEvent{
			TokenID: uuid.New(), Address: "addr1", Delta: "1000",
			BlockCursor: int64(100 + i), TxHash: "tx",
			ActivityType: model.ActivityDeposit, BalanceApplied: true,
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = buildReversalAdjustItems(events)
	})
	// 5 events: grows sub-linearly due to slice pre-alloc → allow up to 30
	assert.LessOrEqual(t, allocs, float64(30), "buildReversalAdjustItems allocs should scale sub-linearly")
}

func TestAllocRegression_BuildPromotionAdjustItems_SingleDeposit(t *testing.T) {
	tokenID := uuid.New()
	events := []promotedEvent{
		{TokenID: tokenID, Address: "addr1", Delta: "3000", BlockCursor: 50, TxHash: "tx1", ActivityType: model.ActivityDeposit},
	}
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = buildPromotionAdjustItems(events)
	})
	assert.LessOrEqual(t, allocs, float64(10), "buildPromotionAdjustItems allocs should stay bounded")
}

func TestAllocRegression_BuildPromotionAdjustItems_FiveEvents(t *testing.T) {
	events := make([]promotedEvent, 5)
	for i := range events {
		events[i] = promotedEvent{
			TokenID: uuid.New(), Address: "addr1", Delta: "1000",
			BlockCursor: int64(50 + i), TxHash: "tx",
			ActivityType: model.ActivityDeposit,
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = buildPromotionAdjustItems(events)
	})
	assert.LessOrEqual(t, allocs, float64(30), "buildPromotionAdjustItems allocs should scale sub-linearly")
}

func TestAllocRegression_BuildReversalAdjustItems_StakeEvent(t *testing.T) {
	events := []rollbackBalanceEvent{
		{TokenID: uuid.New(), Address: "addr1", Delta: "-5000", BlockCursor: 100, TxHash: "tx1", ActivityType: model.ActivityStake, BalanceApplied: true},
	}
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = buildReversalAdjustItems(events)
	})
	// Staking: 2 items (liquid + staked), 2 big.Int parses → allow up to 15
	assert.LessOrEqual(t, allocs, float64(15), "buildReversalAdjustItems staking allocs should stay bounded")
}

func TestAllocRegression_AddDecimalStrings(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = addDecimalStrings("999999999999999999999", "-500000000000000000")
	})
	// big.Int parse x2 + add + string → allow up to 8
	assert.LessOrEqual(t, allocs, float64(8), "addDecimalStrings allocs should stay bounded")
}

func TestAllocRegression_DetectScamSignal_NativeToken(t *testing.T) {
	be := event.NormalizedBalanceEvent{
		TokenType: model.TokenTypeNative,
		Delta:     "-1000",
	}
	allocs := testing.AllocsPerRun(100, func() {
		_ = detectScamSignal(model.ChainSolana, be, "5000", true)
	})
	// Fast path: native token returns "" immediately → zero alloc.
	assert.Equal(t, float64(0), allocs, "detectScamSignal native token fast-path should be zero-alloc")
}

func TestAllocRegression_DetectScamSignal_BTC(t *testing.T) {
	be := event.NormalizedBalanceEvent{
		TokenType: model.TokenTypeFungible,
		Delta:     "-1000",
	}
	allocs := testing.AllocsPerRun(100, func() {
		_ = detectScamSignal(model.ChainBTC, be, "5000", true)
	})
	assert.Equal(t, float64(0), allocs, "detectScamSignal BTC fast-path should be zero-alloc")
}

func TestAllocRegression_DetectScamSignal_NoChainData(t *testing.T) {
	be := event.NormalizedBalanceEvent{
		TokenType: model.TokenTypeFungible,
		Delta:     "1000",
		ChainData: nil,
	}
	allocs := testing.AllocsPerRun(100, func() {
		_ = detectScamSignal(model.ChainBase, be, "5000", true)
	})
	// Positive delta, no chain_data → skip all checks.
	assert.LessOrEqual(t, allocs, float64(1), "detectScamSignal positive delta should have minimal allocs")
}

func TestAllocRegression_DetectScamSignal_WithChainData_NoScamSignal(t *testing.T) {
	chainData := json.RawMessage(`{"some":"metadata"}`)
	be := event.NormalizedBalanceEvent{
		TokenType: model.TokenTypeFungible,
		Delta:     "-1000",
		ChainData: chainData,
	}
	allocs := testing.AllocsPerRun(100, func() {
		_ = detectScamSignal(model.ChainBase, be, "5000", true)
	})
	// bytes.Contains check returns false → no unmarshal → low alloc.
	assert.LessOrEqual(t, allocs, float64(3), "detectScamSignal non-scam should have bounded allocs")
}

func TestAllocRegression_ScanRollbackEvents_FiveRows(t *testing.T) {
	rows := make([]mockRow, 5)
	for i := range rows {
		rows[i] = makeRollbackRow(
			uuid.New(), "addr1", "1000", int64(100+i), "tx",
			nil, nil, model.ActivityDeposit, true,
		)
	}
	scanner := &mockRowScanner{rows: rows}
	allocs := testing.AllocsPerRun(50, func() {
		scanner.idx = 0 // reset scanner for reuse
		_, _ = scanRollbackEvents(scanner)
	})
	// 5 rows: slice growth + NullString vars per row → allow up to 40
	assert.LessOrEqual(t, allocs, float64(40), "scanRollbackEvents allocs should be bounded")
}

func TestAllocRegression_ScanPromotedEvents_FiveRows(t *testing.T) {
	rows := make([]mockRow, 5)
	for i := range rows {
		rows[i] = makePromotedRow(
			uuid.New(), "addr1", "500", int64(50+i), "tx",
			nil, nil, model.ActivityDeposit,
		)
	}
	scanner := &mockRowScanner{rows: rows}
	allocs := testing.AllocsPerRun(50, func() {
		scanner.idx = 0
		_, _ = scanPromotedEvents(scanner)
	})
	assert.LessOrEqual(t, allocs, float64(40), "scanPromotedEvents allocs should be bounded")
}
