package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLRU_BasicGetPut(t *testing.T) {
	c := NewLRU[string, bool](10, 5*time.Minute)

	c.Put("a", true)
	c.Put("b", false)

	v, ok := c.Get("a")
	require.True(t, ok)
	assert.True(t, v)

	v, ok = c.Get("b")
	require.True(t, ok)
	assert.False(t, v)

	_, ok = c.Get("missing")
	assert.False(t, ok)
}

func TestLRU_Eviction(t *testing.T) {
	c := NewLRU[string, int](3, 5*time.Minute)

	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3)

	// Access "a" to make it recently used
	c.Get("a")

	// Adding "d" should evict "b" (least recently used)
	c.Put("d", 4)

	_, ok := c.Get("b")
	assert.False(t, ok, "b should have been evicted")

	v, ok := c.Get("a")
	assert.True(t, ok, "a should still exist")
	assert.Equal(t, 1, v)

	assert.Equal(t, 3, c.Len())
}

func TestLRU_TTLExpiration(t *testing.T) {
	c := NewLRU[string, bool](10, 5*time.Minute)

	now := time.Now()
	c.nowFn = func() time.Time { return now }

	c.Put("a", true)

	// Before expiration
	v, ok := c.Get("a")
	assert.True(t, ok)
	assert.True(t, v)

	// Advance past TTL
	c.nowFn = func() time.Time { return now.Add(6 * time.Minute) }

	_, ok = c.Get("a")
	assert.False(t, ok, "entry should have expired")
}

func TestLRU_UpdateExisting(t *testing.T) {
	c := NewLRU[string, int](10, 5*time.Minute)

	c.Put("a", 1)
	c.Put("a", 2)

	v, ok := c.Get("a")
	require.True(t, ok)
	assert.Equal(t, 2, v)

	assert.Equal(t, 1, c.Len())
}

func TestLRU_Stats(t *testing.T) {
	c := NewLRU[string, bool](10, 5*time.Minute)

	c.Put("a", true)

	c.Get("a")     // hit
	c.Get("a")     // hit
	c.Get("miss")  // miss

	hits, misses := c.Stats()
	assert.Equal(t, int64(2), hits)
	assert.Equal(t, int64(1), misses)
}
