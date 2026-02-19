package cache

import (
	"container/list"
	"sync"
	"time"
)

// LRU is a generic LRU cache with per-entry TTL expiration.
type LRU[K comparable, V any] struct {
	mu       sync.RWMutex
	capacity int
	ttl      time.Duration
	items    map[K]*list.Element
	order    *list.List
	nowFn    func() time.Time

	hits   int64
	misses int64
}

type entry[K comparable, V any] struct {
	key       K
	value     V
	expiresAt time.Time
}

// NewLRU creates a new LRU cache with the given capacity and TTL.
func NewLRU[K comparable, V any](capacity int, ttl time.Duration) *LRU[K, V] {
	return &LRU[K, V]{
		capacity: capacity,
		ttl:      ttl,
		items:    make(map[K]*list.Element, capacity),
		order:    list.New(),
		nowFn:    time.Now,
	}
}

// Get retrieves a value from the cache. Returns the value and true if found
// and not expired; otherwise returns the zero value and false.
func (c *LRU[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		c.misses++
		var zero V
		return zero, false
	}

	e := elem.Value.(*entry[K, V])
	if c.nowFn().After(e.expiresAt) {
		c.removeElement(elem)
		c.misses++
		var zero V
		return zero, false
	}

	c.order.MoveToFront(elem)
	c.hits++
	return e.value, true
}

// Put adds or updates a value in the cache.
func (c *LRU[K, V]) Put(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.order.MoveToFront(elem)
		e := elem.Value.(*entry[K, V])
		e.value = value
		e.expiresAt = c.nowFn().Add(c.ttl)
		return
	}

	if c.order.Len() >= c.capacity {
		c.evictOldest()
	}

	e := &entry[K, V]{
		key:       key,
		value:     value,
		expiresAt: c.nowFn().Add(c.ttl),
	}
	elem := c.order.PushFront(e)
	c.items[key] = elem
}

// Len returns the number of items in the cache (including expired but not yet evicted).
func (c *LRU[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.order.Len()
}

// Stats returns cache hit and miss counts.
func (c *LRU[K, V]) Stats() (hits, misses int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.misses
}

func (c *LRU[K, V]) evictOldest() {
	elem := c.order.Back()
	if elem == nil {
		return
	}
	c.removeElement(elem)
}

func (c *LRU[K, V]) removeElement(elem *list.Element) {
	c.order.Remove(elem)
	e := elem.Value.(*entry[K, V])
	delete(c.items, e.key)
}
