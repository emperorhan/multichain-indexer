//go:build !race

package coordinator

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
//   TickBlockScan 10 addrs:     ~432 ns/op,  10 allocs/op
//   TickBlockScan 1000 addrs: ~16928 ns/op,  10 allocs/op
// ---------------------------------------------------------------------------

func TestSLO_TickBlockScan_10Addrs(t *testing.T) {
	result := testing.Benchmark(func(b *testing.B) {
		addrs := buildWatchedAddresses(10)
		ticks := make([][]model.WatchedAddress, b.N)
		for i := range ticks {
			ticks[i] = addrs
		}

		watchedRepo := &scriptedWatchedAddressRepo{ticks: ticks}
		configRepo := &inMemoryConfigRepo{watermark: 9900}
		jobCh := make(chan event.FetchJob, b.N+1)

		c := New(
			model.ChainBase, model.NetworkMainnet,
			watchedRepo, 100, time.Second,
			jobCh, silentLogger(),
		).
			WithHeadProvider(&stubHeadProvider{head: 10000}).
			WithBlockScanMode(configRepo)

		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = c.tick(ctx)
		}
	})

	t.Logf("TickBlockScan(10 addrs): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(500_000),
		"TickBlockScan(10 addrs) SLO: must complete within 500us (latency ~10x baseline ~432ns)")
	assert.Less(t, result.AllocsPerOp(), int64(50),
		"TickBlockScan(10 addrs) SLO: must allocate fewer than 50 objects (5x baseline ~10)")
}

func TestSLO_TickBlockScan_1000Addrs(t *testing.T) {
	result := testing.Benchmark(func(b *testing.B) {
		addrs := buildWatchedAddresses(1000)
		ticks := make([][]model.WatchedAddress, b.N)
		for i := range ticks {
			ticks[i] = addrs
		}

		watchedRepo := &scriptedWatchedAddressRepo{ticks: ticks}
		configRepo := &inMemoryConfigRepo{watermark: 9900}
		jobCh := make(chan event.FetchJob, b.N+1)

		c := New(
			model.ChainBase, model.NetworkMainnet,
			watchedRepo, 100, time.Second,
			jobCh, silentLogger(),
		).
			WithHeadProvider(&stubHeadProvider{head: 10000}).
			WithBlockScanMode(configRepo)

		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = c.tick(ctx)
		}
	})

	t.Logf("TickBlockScan(1000 addrs): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(5_000_000),
		"TickBlockScan(1000 addrs) SLO: must complete within 5ms (latency ~10x baseline ~17us)")
	assert.Less(t, result.AllocsPerOp(), int64(50),
		"TickBlockScan(1000 addrs) SLO: must allocate fewer than 50 objects (5x baseline ~10)")
}
