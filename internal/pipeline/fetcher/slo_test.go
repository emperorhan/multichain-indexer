//go:build !race

package fetcher

import (
	"context"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/cache"
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
//   FetchSignatures sigs=10:    ~2,052 ns/op,  28 allocs/op
//   FetchSignatures sigs=100:  ~10,272 ns/op,  28 allocs/op
//   FetchSignatures sigs=500:  ~60,455 ns/op,  32 allocs/op
//   FetchTransactions txs=10:   ~2,292 ns/op,  33 allocs/op
//   FetchTransactions txs=100: ~10,901 ns/op,  33 allocs/op
//   FetchTransactions txs=500: ~55,533 ns/op,  37 allocs/op
// ---------------------------------------------------------------------------

func TestSLO_FetchSignatures_10(t *testing.T) {
	result := testing.Benchmark(func(b *testing.B) {
		adapter := newBenchAdapter(10)
		rawBatchCh := make(chan event.RawBatch, 1)
		logger := silentLogger()

		f := &Fetcher{
			adapter:            adapter,
			rawBatchCh:         rawBatchCh,
			logger:             logger,
			retryMaxAttempts:   1,
			batchSizeByAddress: cache.NewLRU[string, int](10000, time.Hour),
		}

		job := event.FetchJob{
			Chain:     model.ChainSolana,
			Network:   model.NetworkDevnet,
			Address:   "bench-addr",
			BatchSize: 10,
		}

		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f.processJob(ctx, logger, job)
			<-rawBatchCh
		}
	})

	t.Logf("FetchSignatures(10): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(10_000),
		"FetchSignatures(10) SLO: must complete within 10us (5x baseline ~2us)")
	assert.Less(t, result.AllocsPerOp(), int64(100),
		"FetchSignatures(10) SLO: must allocate fewer than 100 objects (3x baseline ~28)")
}

func TestSLO_FetchSignatures_100(t *testing.T) {
	result := testing.Benchmark(func(b *testing.B) {
		adapter := newBenchAdapter(100)
		rawBatchCh := make(chan event.RawBatch, 1)
		logger := silentLogger()

		f := &Fetcher{
			adapter:            adapter,
			rawBatchCh:         rawBatchCh,
			logger:             logger,
			retryMaxAttempts:   1,
			batchSizeByAddress: cache.NewLRU[string, int](10000, time.Hour),
		}

		job := event.FetchJob{
			Chain:     model.ChainSolana,
			Network:   model.NetworkDevnet,
			Address:   "bench-addr",
			BatchSize: 100,
		}

		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f.processJob(ctx, logger, job)
			<-rawBatchCh
		}
	})

	t.Logf("FetchSignatures(100): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(50_000),
		"FetchSignatures(100) SLO: must complete within 50us (5x baseline ~10us)")
	assert.Less(t, result.AllocsPerOp(), int64(100),
		"FetchSignatures(100) SLO: must allocate fewer than 100 objects (3x baseline ~28)")
}

func TestSLO_FetchSignatures_500(t *testing.T) {
	result := testing.Benchmark(func(b *testing.B) {
		adapter := newBenchAdapter(500)
		rawBatchCh := make(chan event.RawBatch, 1)
		logger := silentLogger()

		f := &Fetcher{
			adapter:            adapter,
			rawBatchCh:         rawBatchCh,
			logger:             logger,
			retryMaxAttempts:   1,
			batchSizeByAddress: cache.NewLRU[string, int](10000, time.Hour),
		}

		job := event.FetchJob{
			Chain:     model.ChainSolana,
			Network:   model.NetworkDevnet,
			Address:   "bench-addr",
			BatchSize: 500,
		}

		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f.processJob(ctx, logger, job)
			<-rawBatchCh
		}
	})

	t.Logf("FetchSignatures(500): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(300_000),
		"FetchSignatures(500) SLO: must complete within 300us (5x baseline ~60us)")
	assert.Less(t, result.AllocsPerOp(), int64(100),
		"FetchSignatures(500) SLO: must allocate fewer than 100 objects (3x baseline ~32)")
}

func TestSLO_FetchTransactions_10(t *testing.T) {
	result := testing.Benchmark(func(b *testing.B) {
		adapter := newBenchAdapter(10)
		rawBatchCh := make(chan event.RawBatch, 1)
		logger := silentLogger()

		cursor := "sig-000000"
		f := &Fetcher{
			adapter:            adapter,
			rawBatchCh:         rawBatchCh,
			logger:             logger,
			retryMaxAttempts:   1,
			batchSizeByAddress: cache.NewLRU[string, int](10000, time.Hour),
		}

		job := event.FetchJob{
			Chain:          model.ChainSolana,
			Network:        model.NetworkDevnet,
			Address:        "bench-addr",
			CursorValue:    &cursor,
			CursorSequence: 1000,
			BatchSize:      10,
		}

		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f.processJob(ctx, logger, job)
			<-rawBatchCh
		}
	})

	t.Logf("FetchTransactions(10): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(12_000),
		"FetchTransactions(10) SLO: must complete within 12us (5x baseline ~2.3us)")
	assert.Less(t, result.AllocsPerOp(), int64(100),
		"FetchTransactions(10) SLO: must allocate fewer than 100 objects (3x baseline ~33)")
}

func TestSLO_FetchTransactions_100(t *testing.T) {
	result := testing.Benchmark(func(b *testing.B) {
		adapter := newBenchAdapter(100)
		rawBatchCh := make(chan event.RawBatch, 1)
		logger := silentLogger()

		cursor := "sig-000000"
		f := &Fetcher{
			adapter:            adapter,
			rawBatchCh:         rawBatchCh,
			logger:             logger,
			retryMaxAttempts:   1,
			batchSizeByAddress: cache.NewLRU[string, int](10000, time.Hour),
		}

		job := event.FetchJob{
			Chain:          model.ChainSolana,
			Network:        model.NetworkDevnet,
			Address:        "bench-addr",
			CursorValue:    &cursor,
			CursorSequence: 1000,
			BatchSize:      100,
		}

		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f.processJob(ctx, logger, job)
			<-rawBatchCh
		}
	})

	t.Logf("FetchTransactions(100): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(55_000),
		"FetchTransactions(100) SLO: must complete within 55us (5x baseline ~11us)")
	assert.Less(t, result.AllocsPerOp(), int64(100),
		"FetchTransactions(100) SLO: must allocate fewer than 100 objects (3x baseline ~33)")
}

func TestSLO_FetchTransactions_500(t *testing.T) {
	result := testing.Benchmark(func(b *testing.B) {
		adapter := newBenchAdapter(500)
		rawBatchCh := make(chan event.RawBatch, 1)
		logger := silentLogger()

		cursor := "sig-000000"
		f := &Fetcher{
			adapter:            adapter,
			rawBatchCh:         rawBatchCh,
			logger:             logger,
			retryMaxAttempts:   1,
			batchSizeByAddress: cache.NewLRU[string, int](10000, time.Hour),
		}

		job := event.FetchJob{
			Chain:          model.ChainSolana,
			Network:        model.NetworkDevnet,
			Address:        "bench-addr",
			CursorValue:    &cursor,
			CursorSequence: 1000,
			BatchSize:      500,
		}

		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f.processJob(ctx, logger, job)
			<-rawBatchCh
		}
	})

	t.Logf("FetchTransactions(500): %d ns/op, %d allocs/op, %d B/op",
		result.NsPerOp(), result.AllocsPerOp(), result.AllocedBytesPerOp())

	assert.Less(t, result.NsPerOp(), int64(300_000),
		"FetchTransactions(500) SLO: must complete within 300us (5x baseline ~56us)")
	assert.Less(t, result.AllocsPerOp(), int64(120),
		"FetchTransactions(500) SLO: must allocate fewer than 120 objects (3x baseline ~37)")
}
