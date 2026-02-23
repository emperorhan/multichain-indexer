//go:build !race

package ingester

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// SLO (Service Level Objective) gates: fail CI if performance degrades beyond
// thresholds. Thresholds are set at ~5x measured local baseline for latency
// and ~3x for allocation counts to accommodate CI variance and platform
// differences.
//
// Local baselines (Apple M4 Pro, 2026-02-23):
//   ProcessBatch_1Tx:   ~12,000 ns/op,  144 allocs/op
//   ProcessBatch_10Tx:  ~38,000 ns/op,  491 allocs/op
//   ProcessBatch_50Tx: ~144,000 ns/op, 1911 allocs/op
//   ProcessBatch_100Tx:~269,000 ns/op, 3673 allocs/op
// ---------------------------------------------------------------------------

func TestSLO_ProcessBatch_1Tx_Latency(t *testing.T) {
	result := testing.Benchmark(BenchmarkProcessBatch_1Tx)
	nsPerOp := result.NsPerOp()
	allocsPerOp := result.AllocsPerOp()
	t.Logf("ProcessBatch_1Tx: %d ns/op, %d allocs/op, %d B/op",
		nsPerOp, allocsPerOp, result.AllocedBytesPerOp())

	assert.Less(t, nsPerOp, int64(60_000),
		"1Tx batch SLO: must complete within 60us (5x baseline ~12us)")
	assert.Less(t, allocsPerOp, int64(500),
		"1Tx batch SLO: must allocate fewer than 500 objects (3x baseline ~144)")
}

func TestSLO_ProcessBatch_10Tx_Latency(t *testing.T) {
	result := testing.Benchmark(BenchmarkProcessBatch_10Tx)
	nsPerOp := result.NsPerOp()
	allocsPerOp := result.AllocsPerOp()
	t.Logf("ProcessBatch_10Tx: %d ns/op, %d allocs/op, %d B/op",
		nsPerOp, allocsPerOp, result.AllocedBytesPerOp())

	assert.Less(t, nsPerOp, int64(200_000),
		"10Tx batch SLO: must complete within 200us (5x baseline ~38us)")
	assert.Less(t, allocsPerOp, int64(1500),
		"10Tx batch SLO: must allocate fewer than 1500 objects (3x baseline ~491)")
}

func TestSLO_ProcessBatch_50Tx_Latency(t *testing.T) {
	result := testing.Benchmark(BenchmarkProcessBatch_50Tx)
	nsPerOp := result.NsPerOp()
	allocsPerOp := result.AllocsPerOp()
	t.Logf("ProcessBatch_50Tx: %d ns/op, %d allocs/op, %d B/op",
		nsPerOp, allocsPerOp, result.AllocedBytesPerOp())

	assert.Less(t, nsPerOp, int64(750_000),
		"50Tx batch SLO: must complete within 750us (5x baseline ~144us)")
	assert.Less(t, allocsPerOp, int64(6000),
		"50Tx batch SLO: must allocate fewer than 6000 objects (3x baseline ~1911)")
}

func TestSLO_ProcessBatch_100Tx_Latency(t *testing.T) {
	result := testing.Benchmark(BenchmarkProcessBatch_100Tx)
	nsPerOp := result.NsPerOp()
	allocsPerOp := result.AllocsPerOp()
	t.Logf("ProcessBatch_100Tx: %d ns/op, %d allocs/op, %d B/op",
		nsPerOp, allocsPerOp, result.AllocedBytesPerOp())

	assert.Less(t, nsPerOp, int64(1_500_000),
		"100Tx batch SLO: must complete within 1.5ms (5x baseline ~269us)")
	assert.Less(t, allocsPerOp, int64(11000),
		"100Tx batch SLO: must allocate fewer than 11000 objects (3x baseline ~3673)")
}
