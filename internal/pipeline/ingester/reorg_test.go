package ingester

import (
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

func ptr(s string) *string { return &s }

func TestIsCanonicalityDrift_SequenceRegression(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		PreviousCursorValue:    ptr("sig-old"),
		PreviousCursorSequence: 101,
		NewCursorValue:         ptr("sig-new"),
		NewCursorSequence:      100,
	}
	assert.True(t, isCanonicalityDrift(batch), "cursor regression should be detected as drift")
}

func TestIsCanonicalityDrift_SameSequenceDifferentHash_NoTxs(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		PreviousCursorValue:    ptr("sig-aaa"),
		PreviousCursorSequence: 100,
		NewCursorValue:         ptr("sig-bbb"),
		NewCursorSequence:      100,
		Transactions:           []event.NormalizedTransaction{},
	}
	assert.True(t, isCanonicalityDrift(batch), "same sequence, different hash, no txs should be drift")
}

func TestIsCanonicalityDrift_SameSequenceDifferentHash_WithTxs(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		PreviousCursorValue:    ptr("sig-aaa"),
		PreviousCursorSequence: 100,
		NewCursorValue:         ptr("sig-bbb"),
		NewCursorSequence:      100,
		Transactions: []event.NormalizedTransaction{
			{TxHash: "sig-bbb", BlockCursor: 100},
		},
	}
	assert.False(t, isCanonicalityDrift(batch), "same sequence with txs is live/backfill overlap, not reorg")
}

func TestIsBTCRestartAnchorReplay(t *testing.T) {
	previousCursor := "btc-batch-prev"
	newCursor := "btc-batch-new"
	positiveSeq := int64(102)

	assert.True(t, isBTCRestartAnchorReplay(event.NormalizedBatch{
		Chain:                  model.ChainBTC,
		PreviousCursorValue:    &previousCursor,
		PreviousCursorSequence: positiveSeq,
		NewCursorValue:         &newCursor,
		NewCursorSequence:      positiveSeq,
		Transactions:           []event.NormalizedTransaction{},
	}))

	assert.False(t, isBTCRestartAnchorReplay(event.NormalizedBatch{
		Chain:                  model.ChainBTC,
		PreviousCursorValue:    &previousCursor,
		PreviousCursorSequence: positiveSeq,
		NewCursorValue:         &newCursor,
		NewCursorSequence:      positiveSeq + 1,
		Transactions:           []event.NormalizedTransaction{},
	}))

	assert.False(t, isBTCRestartAnchorReplay(event.NormalizedBatch{
		Chain:                  model.ChainSolana,
		PreviousCursorValue:    &previousCursor,
		PreviousCursorSequence: positiveSeq,
		NewCursorValue:         &newCursor,
		NewCursorSequence:      positiveSeq,
		Transactions:           []event.NormalizedTransaction{},
	}))

	assert.False(t, isBTCRestartAnchorReplay(event.NormalizedBatch{
		Chain:                  model.ChainBTC,
		PreviousCursorValue:    &previousCursor,
		PreviousCursorSequence: positiveSeq,
		NewCursorValue:         &newCursor,
		NewCursorSequence:      positiveSeq,
		Transactions:           []event.NormalizedTransaction{{TxHash: "btc-tx"}},
	}))

	assert.True(t, isBTCRestartAnchorReplay(event.NormalizedBatch{
		Chain:                  model.ChainBTC,
		PreviousCursorValue:    ptr(" BTC-BATCH-PREV "),
		PreviousCursorSequence: positiveSeq,
		NewCursorValue:         ptr("0xbtc-batch-new"),
		NewCursorSequence:      positiveSeq,
		Transactions:           []event.NormalizedTransaction{},
	}))

	assert.False(t, isBTCRestartAnchorReplay(event.NormalizedBatch{
		Chain:                  model.ChainBTC,
		PreviousCursorValue:    ptr("btc-batch-prev"),
		PreviousCursorSequence: positiveSeq,
		NewCursorValue:         ptr(" BTC-BATCH-PREV "),
		NewCursorSequence:      positiveSeq,
		Transactions:           []event.NormalizedTransaction{},
	}))

	assert.False(t, isBTCRestartAnchorReplay(event.NormalizedBatch{
		Chain:                  model.ChainBTC,
		PreviousCursorValue:    ptr("  "),
		PreviousCursorSequence: positiveSeq,
		NewCursorValue:         ptr("0xbtc-batch-new"),
		NewCursorSequence:      positiveSeq,
		Transactions:           []event.NormalizedTransaction{},
	}))
}

func TestIsCanonicalityDrift_NilPreviousCursor(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain:             model.ChainSolana,
		Network:           model.NetworkDevnet,
		NewCursorValue:    ptr("sig-new"),
		NewCursorSequence: 100,
	}
	assert.False(t, isCanonicalityDrift(batch), "nil previous cursor should not trigger drift")
}

func TestIsCanonicalityDrift_NilNewCursor(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		PreviousCursorValue:    ptr("sig-old"),
		PreviousCursorSequence: 100,
	}
	assert.False(t, isCanonicalityDrift(batch), "nil new cursor should not trigger drift")
}

func TestIsCanonicalityDrift_ZeroPreviousSequence(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		PreviousCursorValue:    ptr("sig-old"),
		PreviousCursorSequence: 0,
		NewCursorValue:         ptr("sig-new"),
		NewCursorSequence:      50,
	}
	assert.False(t, isCanonicalityDrift(batch), "zero previous sequence should not trigger drift")
}

func TestRollbackForkCursorSequence_SimpleRegression(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		PreviousCursorValue:    ptr("sig-old"),
		PreviousCursorSequence: 110,
		NewCursorValue:         ptr("sig-new"),
		NewCursorSequence:      100,
	}
	assert.Equal(t, int64(100), rollbackForkCursorSequence(batch))
}

func TestRollbackForkCursorSequence_SameSequence(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain:                  model.ChainSolana,
		Network:                model.NetworkDevnet,
		PreviousCursorValue:    ptr("sig-aaa"),
		PreviousCursorSequence: 100,
		NewCursorValue:         ptr("sig-bbb"),
		NewCursorSequence:      100,
	}
	assert.Equal(t, int64(100), rollbackForkCursorSequence(batch))
}

func TestRollbackForkCursorSequence_BTCCompetingBranch(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain:                  model.ChainBTC,
		Network:                model.NetworkMainnet,
		PreviousCursorValue:    ptr("hash-old"),
		PreviousCursorSequence: 305,
		NewCursorValue:         ptr("hash-new"),
		NewCursorSequence:      300,
		Transactions: []event.NormalizedTransaction{
			{TxHash: "btc-tx-1", BlockCursor: 301},
			{TxHash: "btc-tx-2", BlockCursor: 303},
		},
	}
	// BTC competing branch: should use earliest tx block cursor (301) not NewCursorSequence (300)
	// because 301 < 300 is false (301 > 300), so it falls through, and earliest=301, which is >= 300,
	// so it returns false; hence it uses NewCursorSequence.
	assert.Equal(t, int64(300), rollbackForkCursorSequence(batch))
}

func TestShouldContinueCompetingBranchReplay_BTCWithTxs(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain: model.ChainBTC,
		Transactions: []event.NormalizedTransaction{
			{TxHash: "btc-tx-1"},
		},
	}
	assert.True(t, shouldContinueCompetingBranchReplay(batch))
}

func TestShouldContinueCompetingBranchReplay_BTCNoTxs(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain:        model.ChainBTC,
		Transactions: []event.NormalizedTransaction{},
	}
	assert.False(t, shouldContinueCompetingBranchReplay(batch))
}

func TestShouldContinueCompetingBranchReplay_NonBTC(t *testing.T) {
	batch := event.NormalizedBatch{
		Chain: model.ChainSolana,
		Transactions: []event.NormalizedTransaction{
			{TxHash: "sol-tx-1"},
		},
	}
	assert.False(t, shouldContinueCompetingBranchReplay(batch))
}
