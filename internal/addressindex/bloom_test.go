package addressindex

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBloomFilter_AddAndContains(t *testing.T) {
	bf := NewBloomFilter(1000, 0.01)

	bf.Add("alice")
	bf.Add("bob")

	assert.True(t, bf.MayContain("alice"))
	assert.True(t, bf.MayContain("bob"))
	// "charlie" was never added — should be false (with high probability)
	// We test multiple keys to be robust
	falsePositives := 0
	for i := 0; i < 100; i++ {
		if bf.MayContain(fmt.Sprintf("unknown_%d", i)) {
			falsePositives++
		}
	}
	// With 1000 capacity and only 2 items, FP rate should be near zero
	assert.Less(t, falsePositives, 5, "too many false positives for nearly empty filter")
}

func TestBloomFilter_FalsePositiveRate(t *testing.T) {
	n := 10000
	bf := NewBloomFilter(n, 0.01)

	// Add n items
	for i := 0; i < n; i++ {
		bf.Add(fmt.Sprintf("item_%d", i))
	}

	// Verify all added items are found
	for i := 0; i < n; i++ {
		require.True(t, bf.MayContain(fmt.Sprintf("item_%d", i)), "added item must be found")
	}

	// Check FPR on items NOT in the set
	testCount := 100000
	falsePositives := 0
	for i := n; i < n+testCount; i++ {
		if bf.MayContain(fmt.Sprintf("item_%d", i)) {
			falsePositives++
		}
	}

	empiricalFPR := float64(falsePositives) / float64(testCount)
	// Allow up to 1% (target is 0.01, but allow some variance)
	assert.Less(t, empiricalFPR, 0.02, "empirical FPR %f exceeds threshold", empiricalFPR)
	t.Logf("empirical FPR: %.4f%% (%d/%d)", empiricalFPR*100, falsePositives, testCount)
}

func TestBloomFilter_Reset(t *testing.T) {
	bf := NewBloomFilter(1000, 0.01)

	bf.Add("alice")
	require.True(t, bf.MayContain("alice"))

	bf.Reset()

	assert.False(t, bf.MayContain("alice"), "after reset, filter should be empty")
}
