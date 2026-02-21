package addressindex

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/cache"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

// TieredIndexConfig configures the 3-tier address index.
type TieredIndexConfig struct {
	BloomExpectedItems int
	BloomFPR           float64
	LRUCapacity        int
	LRUTTL             time.Duration
}

// TieredIndex implements a 3-tier address lookup:
//
//	Tier 1: Bloom filter — definite negative (O(1), ~17MB for 10M addresses)
//	Tier 2: LRU cache — fast positive/negative cache hit
//	Tier 3: Database — authoritative lookup, result cached in LRU
type TieredIndex struct {
	bloomMu sync.RWMutex
	blooms  map[string]*BloomFilter // keyed by "chain:network"
	lru     cache.Cache[string, *model.WatchedAddress] // nil value = negative cache
	repo    store.WatchedAddressRepository
	cfg     TieredIndexConfig
}

func bloomKey(chain model.Chain, network model.Network) string {
	return string(chain) + ":" + string(network)
}

// NewTieredIndex creates a new 3-tier address index.
func NewTieredIndex(repo store.WatchedAddressRepository, cfg TieredIndexConfig) *TieredIndex {
	if cfg.BloomExpectedItems <= 0 {
		cfg.BloomExpectedItems = 10_000_000
	}
	if cfg.BloomFPR <= 0 {
		cfg.BloomFPR = 0.001
	}
	if cfg.LRUCapacity <= 0 {
		cfg.LRUCapacity = 100_000
	}
	if cfg.LRUTTL <= 0 {
		cfg.LRUTTL = 10 * time.Minute
	}

	return &TieredIndex{
		blooms: make(map[string]*BloomFilter),
		lru:    cache.NewShardedLRU[string, *model.WatchedAddress](cfg.LRUCapacity, cfg.LRUTTL, func(k string) string { return k }),
		repo:   repo,
		cfg:    cfg,
	}
}

// getBloom returns the bloom filter for a chain:network, or nil if none exists.
func (t *TieredIndex) getBloom(chain model.Chain, network model.Network) *BloomFilter {
	t.bloomMu.RLock()
	defer t.bloomMu.RUnlock()
	return t.blooms[bloomKey(chain, network)]
}

func lruKey(chain model.Chain, network model.Network, address string) string {
	return string(chain) + ":" + string(network) + ":" + address
}

// Contains returns true if the address might be watched (bloom + LRU).
// A false return is definitive; a true return may be a false positive.
func (t *TieredIndex) Contains(ctx context.Context, chain model.Chain, network model.Network, address string) bool {
	key := lruKey(chain, network, address)

	// Tier 1: Bloom filter (per chain:network)
	if bf := t.getBloom(chain, network); bf != nil {
		if !bf.MayContain(key) {
			metrics.AddressIndexBloomRejects.WithLabelValues(string(chain), string(network)).Inc()
			return false
		}
	}
	// If no bloom exists yet, skip tier 1 (safe: assume possibly watched)

	// Tier 2: LRU cache
	if wa, ok := t.lru.Get(key); ok {
		metrics.AddressIndexLRUHits.WithLabelValues(string(chain), string(network)).Inc()
		return wa != nil
	}
	metrics.AddressIndexLRUMisses.WithLabelValues(string(chain), string(network)).Inc()

	// Tier 3: DB lookup
	metrics.AddressIndexDBLookups.WithLabelValues(string(chain), string(network)).Inc()
	wa, err := t.repo.FindByAddress(ctx, chain, network, address)
	if err != nil {
		// On error, assume possibly watched to avoid false negatives
		return true
	}

	// Cache result (nil = negative cache)
	t.lru.Put(key, wa)
	return wa != nil
}

// Lookup returns the WatchedAddress if found, applying the 3-tier strategy.
func (t *TieredIndex) Lookup(ctx context.Context, chain model.Chain, network model.Network, address string) *model.WatchedAddress {
	key := lruKey(chain, network, address)

	// Tier 1: Bloom filter (per chain:network)
	if bf := t.getBloom(chain, network); bf != nil {
		if !bf.MayContain(key) {
			metrics.AddressIndexBloomRejects.WithLabelValues(string(chain), string(network)).Inc()
			return nil
		}
	}

	// Tier 2: LRU cache
	if wa, ok := t.lru.Get(key); ok {
		metrics.AddressIndexLRUHits.WithLabelValues(string(chain), string(network)).Inc()
		return wa
	}
	metrics.AddressIndexLRUMisses.WithLabelValues(string(chain), string(network)).Inc()

	// Tier 3: DB lookup
	metrics.AddressIndexDBLookups.WithLabelValues(string(chain), string(network)).Inc()
	wa, err := t.repo.FindByAddress(ctx, chain, network, address)
	if err != nil {
		return nil
	}

	// Cache result (nil = negative cache for bloom FP)
	t.lru.Put(key, wa)
	return wa
}

// Reload rebuilds the bloom filter for a specific chain:network and warms the LRU.
// Other chains' bloom filters are not affected.
func (t *TieredIndex) Reload(ctx context.Context, chain model.Chain, network model.Network) error {
	addrs, err := t.repo.GetActive(ctx, chain, network)
	if err != nil {
		return fmt.Errorf("reload address index: %w", err)
	}

	// Size the bloom filter based on actual address count (with headroom)
	// instead of the static config default, to avoid wasting memory.
	bloomSize := len(addrs) * 10
	if bloomSize < 1000 {
		bloomSize = 1000
	}
	if bloomSize > t.cfg.BloomExpectedItems {
		bloomSize = t.cfg.BloomExpectedItems
	}
	bf := NewBloomFilter(bloomSize, t.cfg.BloomFPR)
	for i := range addrs {
		key := lruKey(chain, network, addrs[i].Address)
		bf.Add(key)
		t.lru.Put(key, &addrs[i])
	}

	// Swap in the new bloom (other chains unaffected).
	t.bloomMu.Lock()
	t.blooms[bloomKey(chain, network)] = bf
	t.bloomMu.Unlock()

	return nil
}
