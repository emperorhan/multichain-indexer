package normalizer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Allocation regression tests for normalizer hot paths.
// ---------------------------------------------------------------------------

func TestAllocRegression_CanonicalizeFinalityState_FastPath(t *testing.T) {
	// Fast path: already-canonical values should produce 0 allocations.
	for _, state := range []string{"processed", "confirmed", "safe", "finalized"} {
		allocs := testing.AllocsPerRun(100, func() {
			_ = canonicalizeFinalityState(state)
		})
		assert.Equal(t, float64(0), allocs, "canonicalizeFinalityState(%q) fast path should be zero-alloc", state)
	}
}

func TestAllocRegression_CanonicalizeFinalityState_SlowPath(t *testing.T) {
	// Slow path: non-canonical values need lowering + trimming.
	for _, state := range []string{"Finalized", "CONFIRMED", " safe ", "Pending"} {
		allocs := testing.AllocsPerRun(100, func() {
			_ = canonicalizeFinalityState(state)
		})
		// strings.ToLower + strings.TrimSpace → allow up to 4 allocs
		assert.LessOrEqual(t, allocs, float64(4), "canonicalizeFinalityState(%q) slow path allocs should be bounded", state)
	}
}
