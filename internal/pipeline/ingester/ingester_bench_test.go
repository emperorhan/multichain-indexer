package ingester

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

func buildBenchBatch(txCount int) event.NormalizedBatch {
	cursor := "bench-cursor"
	txs := make([]event.NormalizedTransaction, txCount)
	for i := 0; i < txCount; i++ {
		txHash := fmt.Sprintf("bench-sig-%d", i)
		txs[i] = event.NormalizedTransaction{
			TxHash:      txHash,
			BlockCursor: int64(100 + i),
			FeeAmount:   "5000",
			FeePayer:    "bench-addr",
			Status:      model.TxStatusSuccess,
			ChainData:   json.RawMessage("{}"),
			BalanceEvents: []event.NormalizedBalanceEvent{
				{
					OuterInstructionIndex: 0,
					InnerInstructionIndex: -1,
					ActivityType:          model.ActivityDeposit,
					EventAction:           "transfer",
					ProgramID:             "11111111111111111111111111111111",
					ContractAddress:       "11111111111111111111111111111111",
					Address:               "bench-addr",
					CounterpartyAddress:   "other-addr",
					Delta:                 "1000000",
					EventID:               fmt.Sprintf("bench-transfer-%d", i),
					TokenType:             model.TokenTypeNative,
				},
				{
					OuterInstructionIndex: -1,
					InnerInstructionIndex: -1,
					ActivityType:          model.ActivityFee,
					EventAction:           "fee",
					ProgramID:             "11111111111111111111111111111111",
					ContractAddress:       "11111111111111111111111111111111",
					Address:               "bench-addr",
					CounterpartyAddress:   "",
					Delta:                 "-5000",
					EventID:               fmt.Sprintf("bench-fee-%d", i),
					TokenType:             model.TokenTypeNative,
				},
			},
		}
	}
	return event.NormalizedBatch{
		Chain:             model.ChainSolana,
		Network:           model.NetworkDevnet,
		Address:           "bench-addr",
		NewCursorValue:    &cursor,
		NewCursorSequence: int64(100 + txCount),
		Transactions:      txs,
	}
}

func newBenchIngester(b *testing.B) (*Ingester, func() *interleaveState) {
	b.Helper()
	db, err := sql.Open("fake_ingester", "")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })

	newState := func() *interleaveState {
		return newInterleaveState(nil)
	}

	state := newState()
	ing := New(
		&interleaveTxBeginner{db: db},
		&interleaveTxRepo{state: state},
		&interleaveBalanceEventRepo{state: state},
		&interleaveBalanceRepo{state: state},
		&interleaveTokenRepo{state: state},
		&interleaveConfigRepo{state: state},
		nil,
		slog.Default(),
	)

	resetState := func() *interleaveState {
		s := newState()
		ing.txRepo = &interleaveTxRepo{state: s}
		ing.balanceEventRepo = &interleaveBalanceEventRepo{state: s}
		ing.balanceRepo = &interleaveBalanceRepo{state: s}
		ing.tokenRepo = &interleaveTokenRepo{state: s}
		ing.configRepo = &interleaveConfigRepo{state: s}
		return s
	}

	return ing, resetState
}

func BenchmarkProcessBatch_1Tx(b *testing.B) {
	ing, resetState := newBenchIngester(b)
	batch := buildBenchBatch(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resetState()
		_ = ing.processBatch(context.Background(), batch)
	}
}

func BenchmarkProcessBatch_10Tx(b *testing.B) {
	ing, resetState := newBenchIngester(b)
	batch := buildBenchBatch(10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resetState()
		_ = ing.processBatch(context.Background(), batch)
	}
}

func BenchmarkProcessBatch_50Tx(b *testing.B) {
	ing, resetState := newBenchIngester(b)
	batch := buildBenchBatch(50)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resetState()
		_ = ing.processBatch(context.Background(), batch)
	}
}

func BenchmarkProcessBatch_100Tx(b *testing.B) {
	ing, resetState := newBenchIngester(b)
	batch := buildBenchBatch(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resetState()
		_ = ing.processBatch(context.Background(), batch)
	}
}
