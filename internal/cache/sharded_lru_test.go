package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestShardedLRU_BasicOps(t *testing.T) {
	c := NewShardedLRU[string, int](100, time.Minute, func(k string) string { return k })

	// Put and Get
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3)

	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", v, ok)
	}
	v, ok = c.Get("b")
	if !ok || v != 2 {
		t.Fatalf("expected (2, true), got (%d, %v)", v, ok)
	}

	// Miss
	_, ok = c.Get("z")
	if ok {
		t.Fatal("expected miss for key 'z'")
	}

	// Len
	if c.Len() != 3 {
		t.Fatalf("expected Len()=3, got %d", c.Len())
	}
}

func TestShardedLRU_Update(t *testing.T) {
	c := NewShardedLRU[string, int](100, time.Minute, func(k string) string { return k })

	c.Put("x", 10)
	c.Put("x", 20)

	v, ok := c.Get("x")
	if !ok || v != 20 {
		t.Fatalf("expected (20, true), got (%d, %v)", v, ok)
	}
	if c.Len() != 1 {
		t.Fatalf("expected Len()=1, got %d", c.Len())
	}
}

func TestShardedLRU_Eviction(t *testing.T) {
	// Small capacity: 16 shards × 1 per shard = 16 total
	c := NewShardedLRU[string, int](16, time.Minute, func(k string) string { return k })

	// Insert many more than capacity
	for i := 0; i < 100; i++ {
		c.Put(fmt.Sprintf("key-%d", i), i)
	}

	// Total items should be ≤ 16 (one per shard)
	if c.Len() > 16 {
		t.Fatalf("expected Len() <= 16, got %d", c.Len())
	}
}

func TestShardedLRU_Stats(t *testing.T) {
	c := NewShardedLRU[string, int](100, time.Minute, func(k string) string { return k })

	c.Put("a", 1)
	c.Get("a") // hit
	c.Get("b") // miss

	hits, misses := c.Stats()
	if hits != 1 {
		t.Fatalf("expected 1 hit, got %d", hits)
	}
	if misses != 1 {
		t.Fatalf("expected 1 miss, got %d", misses)
	}
}

func TestShardedLRU_ConcurrentAccess(t *testing.T) {
	c := NewShardedLRU[string, int](10000, time.Minute, func(k string) string { return k })

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				key := fmt.Sprintf("g%d-k%d", id, i)
				c.Put(key, i)
				c.Get(key)
			}
		}(g)
	}
	wg.Wait()

	if c.Len() == 0 {
		t.Fatal("expected non-zero Len after concurrent writes")
	}
}

func TestShardedLRU_ImplementsCache(t *testing.T) {
	var _ Cache[string, int] = NewShardedLRU[string, int](100, time.Minute, func(k string) string { return k })
	var _ Cache[string, int] = NewLRU[string, int](100, time.Minute)
}
