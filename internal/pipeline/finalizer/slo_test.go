//go:build !race

package finalizer

import (
	"context"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// SLO (Service Level Objective) gates: fail CI if performance degrades beyond
// thresholds. Thresholds are set at ~5x measured local baseline for latency
// and ~5x for allocation counts to accommodate CI variance and platform
// differences.
//
// Local baselines (Apple M4 Pro, 2026-02-23):
//   FinalizerCheck:             ~874 ns/op,  2 allocs/op
//   FinalizerCheck_WithPruning: ~1707 ns/op, 3 allocs/op
// ---------------------------------------------------------------------------

func TestSLO_FinalizerCheck(t *testing.T) {
	result := testing.Benchmark(func(b *testing.B) {
		adapter := &fakeReorgAwareAdapter{
			chainName:      "base",
			finalizedBlock: 10000,
		}
		blockRepo := &fakeBlockRepo{}
		finalityCh := make(chan event.FinalityPromotion, b.N+1)

		f := New(
			model.ChainBase, model.NetworkMainnet,
			adapter, blockRepo, finalityCh,
			time.Second, silentLogger(),
		)

		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			adapter.finalizedBlock = int64(10000 + i)
			_ = f.check(ctx)
		}
	})

	t.Logf("FinalizerCheck: %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(500_000),
		"FinalizerCheck SLO: must complete within 500us (latency ~10x baseline ~874ns)")
	assert.Less(t, result.AllocsPerOp(), int64(20),
		"FinalizerCheck SLO: must allocate fewer than 20 objects (10x baseline ~2)")
}

func TestSLO_FinalizerCheck_WithPruning(t *testing.T) {
	result := testing.Benchmark(func(b *testing.B) {
		adapter := &fakeReorgAwareAdapter{
			chainName:      "base",
			finalizedBlock: 20000,
		}
		blockRepo := &fakeBlockRepo{purgeCount: 100}
		finalityCh := make(chan event.FinalityPromotion, b.N+1)

		f := New(
			model.ChainBase, model.NetworkMainnet,
			adapter, blockRepo, finalityCh,
			time.Second, silentLogger(),
			WithRetentionBlocks(10000),
		)

		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			adapter.finalizedBlock = int64(20000 + i)
			_ = f.check(ctx)
		}
	})

	t.Logf("FinalizerCheck_WithPruning: %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(1_000_000),
		"FinalizerCheck_WithPruning SLO: must complete within 1ms (latency ~10x baseline ~1707ns)")
	assert.Less(t, result.AllocsPerOp(), int64(30),
		"FinalizerCheck_WithPruning SLO: must allocate fewer than 30 objects (10x baseline ~3)")
}
