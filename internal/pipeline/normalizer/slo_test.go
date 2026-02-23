//go:build !race

package normalizer

import (
	"fmt"
	"log/slog"
	"testing"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// SLO (Service Level Objective) gates: fail CI if performance degrades beyond
// thresholds. Thresholds are set at ~5x measured local baseline for latency
// and ~3x for allocation counts to accommodate CI variance and platform
// differences.
//
// Local baselines (Apple M4 Pro, 2026-02-23):
//   BuildCanonicalSolanaBalanceEvents events_1:   ~2,122 ns/op,   64 allocs/op
//   BuildCanonicalSolanaBalanceEvents events_5:   ~6,787 ns/op,  200 allocs/op
//   BuildCanonicalSolanaBalanceEvents events_20: ~26,433 ns/op,  721 allocs/op
//   BuildCanonicalBaseBalanceEvents events_1:     ~5,975 ns/op,  155 allocs/op
//   BuildCanonicalBaseBalanceEvents events_5:    ~16,924 ns/op,  427 allocs/op
//   BuildCanonicalBaseBalanceEvents events_20:   ~59,751 ns/op, 1452 allocs/op
//   NormalizedTxFromResult events_5:              ~6,892 ns/op,  201 allocs/op
//   NormalizedTxFromResult_EVM:                  ~18,074 ns/op,  429 allocs/op
// ---------------------------------------------------------------------------

func TestSLO_BuildCanonicalSolanaBalanceEvents_1(t *testing.T) {
	const watchedAddr = "SoLAddr1111111111111111111111111111111111111"
	rawEvents := makeSolanaBalanceEventInfos(watchedAddr, 1)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = buildCanonicalSolanaBalanceEvents(
				model.ChainSolana, model.NetworkDevnet,
				"txhash_bench_solana", "SUCCESS", watchedAddr,
				"5000", "finalized", rawEvents, watchedAddr,
			)
		}
	})

	t.Logf("BuildCanonicalSolanaBalanceEvents(1): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(12_000),
		"Solana BEs(1) SLO: must complete within 12us (5x baseline ~2.1us)")
	assert.Less(t, result.AllocsPerOp(), int64(200),
		"Solana BEs(1) SLO: must allocate fewer than 200 objects (3x baseline ~64)")
}

func TestSLO_BuildCanonicalSolanaBalanceEvents_5(t *testing.T) {
	const watchedAddr = "SoLAddr1111111111111111111111111111111111111"
	rawEvents := makeSolanaBalanceEventInfos(watchedAddr, 5)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = buildCanonicalSolanaBalanceEvents(
				model.ChainSolana, model.NetworkDevnet,
				"txhash_bench_solana", "SUCCESS", watchedAddr,
				"5000", "finalized", rawEvents, watchedAddr,
			)
		}
	})

	t.Logf("BuildCanonicalSolanaBalanceEvents(5): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(35_000),
		"Solana BEs(5) SLO: must complete within 35us (5x baseline ~6.8us)")
	assert.Less(t, result.AllocsPerOp(), int64(600),
		"Solana BEs(5) SLO: must allocate fewer than 600 objects (3x baseline ~200)")
}

func TestSLO_BuildCanonicalSolanaBalanceEvents_20(t *testing.T) {
	const watchedAddr = "SoLAddr1111111111111111111111111111111111111"
	rawEvents := makeSolanaBalanceEventInfos(watchedAddr, 20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = buildCanonicalSolanaBalanceEvents(
				model.ChainSolana, model.NetworkDevnet,
				"txhash_bench_solana", "SUCCESS", watchedAddr,
				"5000", "finalized", rawEvents, watchedAddr,
			)
		}
	})

	t.Logf("BuildCanonicalSolanaBalanceEvents(20): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(135_000),
		"Solana BEs(20) SLO: must complete within 135us (5x baseline ~26us)")
	assert.Less(t, result.AllocsPerOp(), int64(2200),
		"Solana BEs(20) SLO: must allocate fewer than 2200 objects (3x baseline ~721)")
}

func TestSLO_BuildCanonicalBaseBalanceEvents_1(t *testing.T) {
	const watchedAddr = "0xdead000000000000000000000000000000000001"
	rawEvents := makeEVMBalanceEventInfos(watchedAddr, 1)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = buildCanonicalBaseBalanceEvents(
				model.ChainBase, model.NetworkMainnet,
				"0xdeadbeef0000000000000000000000000000000000000000000000000000abcd",
				"SUCCESS", watchedAddr, "26000", "finalized", rawEvents, watchedAddr,
			)
		}
	})

	t.Logf("BuildCanonicalBaseBalanceEvents(1): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(30_000),
		"Base BEs(1) SLO: must complete within 30us (5x baseline ~6us)")
	assert.Less(t, result.AllocsPerOp(), int64(500),
		"Base BEs(1) SLO: must allocate fewer than 500 objects (3x baseline ~155)")
}

func TestSLO_BuildCanonicalBaseBalanceEvents_5(t *testing.T) {
	const watchedAddr = "0xdead000000000000000000000000000000000001"
	rawEvents := makeEVMBalanceEventInfos(watchedAddr, 5)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = buildCanonicalBaseBalanceEvents(
				model.ChainBase, model.NetworkMainnet,
				"0xdeadbeef0000000000000000000000000000000000000000000000000000abcd",
				"SUCCESS", watchedAddr, "26000", "finalized", rawEvents, watchedAddr,
			)
		}
	})

	t.Logf("BuildCanonicalBaseBalanceEvents(5): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(85_000),
		"Base BEs(5) SLO: must complete within 85us (5x baseline ~17us)")
	assert.Less(t, result.AllocsPerOp(), int64(1300),
		"Base BEs(5) SLO: must allocate fewer than 1300 objects (3x baseline ~427)")
}

func TestSLO_BuildCanonicalBaseBalanceEvents_20(t *testing.T) {
	const watchedAddr = "0xdead000000000000000000000000000000000001"
	rawEvents := makeEVMBalanceEventInfos(watchedAddr, 20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = buildCanonicalBaseBalanceEvents(
				model.ChainBase, model.NetworkMainnet,
				"0xdeadbeef0000000000000000000000000000000000000000000000000000abcd",
				"SUCCESS", watchedAddr, "26000", "finalized", rawEvents, watchedAddr,
			)
		}
	})

	t.Logf("BuildCanonicalBaseBalanceEvents(20): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(300_000),
		"Base BEs(20) SLO: must complete within 300us (5x baseline ~60us)")
	assert.Less(t, result.AllocsPerOp(), int64(4400),
		"Base BEs(20) SLO: must allocate fewer than 4400 objects (3x baseline ~1452)")
}

func TestSLO_NormalizedTxFromResult_Solana_5(t *testing.T) {
	const watchedAddr = "SoLAddr1111111111111111111111111111111111111"
	rawEvents := makeSolanaBalanceEventInfos(watchedAddr, 5)
	txResult := &sidecarv1.TransactionResult{
		TxHash:        "txhash_bench_norm",
		BlockCursor:   12345,
		BlockTime:     1700000000,
		FeeAmount:     "5000",
		FeePayer:      watchedAddr,
		Status:        "SUCCESS",
		BalanceEvents: rawEvents,
	}
	batch := event.RawBatch{
		Chain:   model.ChainSolana,
		Network: model.NetworkDevnet,
		Address: watchedAddr,
	}
	watchedAddrs := resolveWatchedAddressSet(batch)

	n := &Normalizer{
		logger: slog.Default(),
	}

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = n.normalizedTxFromResult(batch, txResult, nil, watchedAddrs)
		}
	})

	t.Logf("NormalizedTxFromResult_Solana(5): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(35_000),
		"NormalizedTxFromResult Solana(5) SLO: must complete within 35us (5x baseline ~7us)")
	assert.Less(t, result.AllocsPerOp(), int64(600),
		"NormalizedTxFromResult Solana(5) SLO: must allocate fewer than 600 objects (3x baseline ~201)")
}

func TestSLO_NormalizedTxFromResult_EVM(t *testing.T) {
	const watchedAddr = "0xdead000000000000000000000000000000000001"
	rawEvents := makeEVMBalanceEventInfos(watchedAddr, 5)
	txResult := &sidecarv1.TransactionResult{
		TxHash:        "0xdeadbeef00000000000000000000000000000000000000000000000000001234",
		BlockCursor:   99999,
		BlockTime:     1700000000,
		FeeAmount:     "26000",
		FeePayer:      watchedAddr,
		Status:        "SUCCESS",
		BalanceEvents: rawEvents,
	}
	batch := event.RawBatch{
		Chain:   model.ChainBase,
		Network: model.NetworkMainnet,
		Address: watchedAddr,
	}
	watchedAddrs := resolveWatchedAddressSet(batch)

	n := &Normalizer{
		logger: slog.Default(),
	}

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = n.normalizedTxFromResult(batch, txResult, nil, watchedAddrs)
		}
	})

	t.Logf("NormalizedTxFromResult_EVM: %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(90_000),
		"NormalizedTxFromResult EVM SLO: must complete within 90us (5x baseline ~18us)")
	assert.Less(t, result.AllocsPerOp(), int64(1300),
		"NormalizedTxFromResult EVM SLO: must allocate fewer than 1300 objects (3x baseline ~429)")
}

func TestSLO_CanonicalizeBatchSignatures_50(t *testing.T) {
	sigs := makeSignatureInfos(50)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = canonicalizeBatchSignatures(model.ChainSolana, sigs)
		}
	})

	t.Logf("CanonicalizeBatchSignatures(50): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(20_000),
		"CanonicalizeBatchSignatures(50) SLO: must complete within 20us (5x baseline ~3.8us)")
	assert.Less(t, result.AllocsPerOp(), int64(25),
		"CanonicalizeBatchSignatures(50) SLO: must allocate fewer than 25 objects (3x baseline ~7)")
}

func TestSLO_CanonicalizeBatchSignatures_EVM_50(t *testing.T) {
	evmSigs := make([]event.SignatureInfo, 50)
	for i := range evmSigs {
		evmSigs[i] = event.SignatureInfo{
			Hash:     fmt.Sprintf("0xABCDEF%040d", i),
			Sequence: int64(i),
		}
	}

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = canonicalizeBatchSignatures(model.ChainEthereum, evmSigs)
		}
	})

	t.Logf("CanonicalizeBatchSignatures_EVM(50): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(42_000),
		"CanonicalizeBatchSignatures_EVM(50) SLO: must complete within 42us (5x baseline ~8.2us)")
	assert.Less(t, result.AllocsPerOp(), int64(350),
		"CanonicalizeBatchSignatures_EVM(50) SLO: must allocate fewer than 350 objects (3x baseline ~107)")
}
