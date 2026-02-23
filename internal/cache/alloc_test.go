package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Allocation regression tests for LRU cache hot paths.
// ---------------------------------------------------------------------------

func TestAllocRegression_LRU_Get_Hit(t *testing.T) {
	lru := NewLRU[string, int](1000, 5*time.Minute)
	lru.Put("hit-key", 42)

	allocs := testing.AllocsPerRun(100, func() {
		lru.Get("hit-key")
	})
	// Cache hit should be zero-alloc (no new objects created).
	assert.Equal(t, float64(0), allocs, "LRU.Get cache hit should be zero-alloc")
}

func TestAllocRegression_LRU_Get_Miss(t *testing.T) {
	lru := NewLRU[string, int](1000, 5*time.Minute)

	allocs := testing.AllocsPerRun(100, func() {
		lru.Get("miss-key")
	})
	// Cache miss should be zero-alloc (just a map lookup).
	assert.Equal(t, float64(0), allocs, "LRU.Get cache miss should be zero-alloc")
}

func TestAllocRegression_LRU_Put_Existing(t *testing.T) {
	lru := NewLRU[string, int](1000, 5*time.Minute)
	lru.Put("key", 1)

	allocs := testing.AllocsPerRun(100, func() {
		lru.Put("key", 2)
	})
	// Updating existing key should not allocate new entry nodes.
	assert.LessOrEqual(t, allocs, float64(1), "LRU.Put existing key should have minimal allocs")
}
