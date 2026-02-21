package addressindex

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sync"
)

// BloomFilter is a thread-safe bloom filter using double-hashing (FNV-128a split into h1, h2).
type BloomFilter struct {
	mu   sync.RWMutex
	bits []uint64
	m    uint64 // total bits
	k    uint   // number of hash functions
}

// NewBloomFilter creates a bloom filter sized for expectedItems with the given
// false positive rate (fpr). For example, 10M items at 0.001 FPR ≈ 17MB.
func NewBloomFilter(expectedItems int, fpr float64) *BloomFilter {
	if expectedItems <= 0 {
		expectedItems = 1
	}
	if fpr <= 0 || fpr >= 1 {
		fpr = 0.001
	}

	n := float64(expectedItems)
	// Optimal bit count: m = -n*ln(p) / (ln(2))^2
	m := uint64(math.Ceil(-n * math.Log(fpr) / (math.Ln2 * math.Ln2)))
	// Optimal hash count: k = (m/n) * ln(2)
	k := uint(math.Ceil(float64(m) / n * math.Ln2))
	if k < 1 {
		k = 1
	}

	words := (m + 63) / 64
	return &BloomFilter{
		bits: make([]uint64, words),
		m:    m,
		k:    k,
	}
}

// Add inserts a key into the bloom filter.
func (bf *BloomFilter) Add(key string) {
	h1, h2 := bf.hash(key)
	bf.mu.Lock()
	for i := uint(0); i < bf.k; i++ {
		pos := (h1 + uint64(i)*h2) % bf.m
		bf.bits[pos/64] |= 1 << (pos % 64)
	}
	bf.mu.Unlock()
}

// MayContain returns false if the key is definitely not in the set.
// Returns true if the key is probably in the set (subject to FPR).
func (bf *BloomFilter) MayContain(key string) bool {
	h1, h2 := bf.hash(key)
	bf.mu.RLock()
	defer bf.mu.RUnlock()
	for i := uint(0); i < bf.k; i++ {
		pos := (h1 + uint64(i)*h2) % bf.m
		if bf.bits[pos/64]&(1<<(pos%64)) == 0 {
			return false
		}
	}
	return true
}

// hash produces two independent 64-bit hashes from FNV-128a.
func (bf *BloomFilter) hash(key string) (uint64, uint64) {
	h := fnv.New128a()
	h.Write([]byte(key))
	sum := h.Sum(nil) // 16 bytes
	h1 := binary.BigEndian.Uint64(sum[:8])
	h2 := binary.BigEndian.Uint64(sum[8:])
	if h2 == 0 {
		h2 = 1 // Avoid degenerate case
	}
	return h1, h2
}
