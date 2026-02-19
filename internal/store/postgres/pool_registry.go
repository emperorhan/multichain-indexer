package postgres

import (
	"fmt"
	"sync"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// poolKey builds a deterministic map key from chain + network.
func poolKey(chain model.Chain, network model.Network) string {
	return fmt.Sprintf("%s-%s", chain, network)
}

// PoolRegistry maintains per-chain-network DB connection pools and falls back
// to a default pool when no chain-specific override is registered.
type PoolRegistry struct {
	mu        sync.RWMutex
	defaultDB *DB
	pools     map[string]*DB
}

// NewPoolRegistry creates a PoolRegistry with the given default DB.
// The default DB is used as the fallback for any chain-network pair that has
// not been explicitly registered.
func NewPoolRegistry(defaultDB *DB) *PoolRegistry {
	return &PoolRegistry{
		defaultDB: defaultDB,
		pools:     make(map[string]*DB),
	}
}

// Register associates a dedicated DB connection pool with the given
// chain-network pair.  It overwrites any previously registered pool for the
// same key (the caller is responsible for closing the replaced pool if needed).
func (r *PoolRegistry) Register(chain model.Chain, network model.Network, db *DB) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pools[poolKey(chain, network)] = db
}

// Get returns the DB pool for the given chain-network pair.  If no
// chain-specific pool has been registered, the default DB is returned.
func (r *PoolRegistry) Get(chain model.Chain, network model.Network) *DB {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if db, ok := r.pools[poolKey(chain, network)]; ok {
		return db
	}
	return r.defaultDB
}

// Close closes all chain-specific DB pools that were registered via Register.
// It does NOT close the default DB — the caller owns the default DB's lifecycle.
// Close returns the first error encountered, but still attempts to close every
// registered pool.
func (r *PoolRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var firstErr error
	for key, db := range r.pools {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close pool %s: %w", key, err)
		}
	}
	// Clear the map so double-close is harmless.
	r.pools = make(map[string]*DB)
	return firstErr
}
