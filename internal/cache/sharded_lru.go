package cache

import (
	"hash/fnv"
	"time"
)

const defaultShardCount = 16

// Cache is the common interface for LRU and ShardedLRU caches.
type Cache[K comparable, V any] interface {
	Get(key K) (V, bool)
	Put(key K, value V)
	Len() int
	Stats() (hits, misses int64)
}

// Compile-time check that LRU satisfies Cache.
var _ Cache[string, int] = (*LRU[string, int])(nil)

// ShardedLRU distributes keys across multiple LRU shards to reduce lock contention.
// It uses FNV-32a hashing on the string representation of the key to select a shard.
type ShardedLRU[K comparable, V any] struct {
	shards   []*LRU[K, V]
	count    int
	keyToStr func(K) string
}

// NewShardedLRU creates a ShardedLRU with defaultShardCount shards.
// totalCapacity is distributed evenly across shards. keyFn converts
// a key to a string for shard selection via FNV-32a.
func NewShardedLRU[K comparable, V any](totalCapacity int, ttl time.Duration, keyFn func(K) string) *ShardedLRU[K, V] {
	return NewShardedLRUWithCount[K, V](totalCapacity, ttl, keyFn, defaultShardCount)
}

// NewShardedLRUWithCount creates a ShardedLRU with a specified shard count.
func NewShardedLRUWithCount[K comparable, V any](totalCapacity int, ttl time.Duration, keyFn func(K) string, shardCount int) *ShardedLRU[K, V] {
	if shardCount <= 0 {
		shardCount = defaultShardCount
	}
	perShard := totalCapacity / shardCount
	if perShard < 1 {
		perShard = 1
	}

	shards := make([]*LRU[K, V], shardCount)
	for i := range shards {
		shards[i] = NewLRU[K, V](perShard, ttl)
	}

	return &ShardedLRU[K, V]{
		shards:   shards,
		count:    shardCount,
		keyToStr: keyFn,
	}
}

func (s *ShardedLRU[K, V]) shard(key K) *LRU[K, V] {
	h := fnv.New32a()
	h.Write([]byte(s.keyToStr(key)))
	return s.shards[int(h.Sum32())%s.count]
}

// Get retrieves a value from the appropriate shard.
func (s *ShardedLRU[K, V]) Get(key K) (V, bool) {
	return s.shard(key).Get(key)
}

// Put adds or updates a value in the appropriate shard.
func (s *ShardedLRU[K, V]) Put(key K, value V) {
	s.shard(key).Put(key, value)
}

// Len returns the total number of items across all shards.
func (s *ShardedLRU[K, V]) Len() int {
	total := 0
	for _, sh := range s.shards {
		total += sh.Len()
	}
	return total
}

// Stats returns aggregated hit and miss counts across all shards.
func (s *ShardedLRU[K, V]) Stats() (hits, misses int64) {
	for _, sh := range s.shards {
		h, m := sh.Stats()
		hits += h
		misses += m
	}
	return
}

// Compile-time check that ShardedLRU satisfies Cache.
var _ Cache[string, int] = (*ShardedLRU[string, int])(nil)
