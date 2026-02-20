package addressindex

import (
	"context"
	"fmt"
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
	bloom *BloomFilter
	lru   *cache.LRU[string, *model.WatchedAddress] // nil value = negative cache
	repo  store.WatchedAddressRepository
	cfg   TieredIndexConfig
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
		bloom: NewBloomFilter(cfg.BloomExpectedItems, cfg.BloomFPR),
		lru:   cache.NewLRU[string, *model.WatchedAddress](cfg.LRUCapacity, cfg.LRUTTL),
		repo:  repo,
		cfg:   cfg,
	}
}

func lruKey(chain model.Chain, network model.Network, address string) string {
	return fmt.Sprintf("%s:%s:%s", chain, network, address)
}

// Contains returns true if the address might be watched (bloom + LRU).
// A false return is definitive; a true return may be a false positive.
func (t *TieredIndex) Contains(ctx context.Context, chain model.Chain, network model.Network, address string) bool {
	key := lruKey(chain, network, address)

	// Tier 1: Bloom filter
	if !t.bloom.MayContain(key) {
		metrics.AddressIndexBloomRejects.WithLabelValues(string(chain), string(network)).Inc()
		return false
	}

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

	// Tier 1: Bloom filter
	if !t.bloom.MayContain(key) {
		metrics.AddressIndexBloomRejects.WithLabelValues(string(chain), string(network)).Inc()
		return nil
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

// Reload rebuilds the bloom filter and warms the LRU from the database.
func (t *TieredIndex) Reload(ctx context.Context, chain model.Chain, network model.Network) error {
	addrs, err := t.repo.GetActive(ctx, chain, network)
	if err != nil {
		return fmt.Errorf("reload address index: %w", err)
	}

	t.bloom.Reset()
	for i := range addrs {
		key := lruKey(chain, network, addrs[i].Address)
		t.bloom.Add(key)
		t.lru.Put(key, &addrs[i])
	}

	return nil
}
