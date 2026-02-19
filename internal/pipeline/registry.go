package pipeline

import (
	"sync"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// Registry maps chain/network pairs to their running Pipeline instances.
// It is used by the Admin API to route replay requests to the correct pipeline.
type Registry struct {
	mu        sync.RWMutex
	pipelines map[string]*Pipeline
}

// NewRegistry creates a new empty pipeline registry.
func NewRegistry() *Registry {
	return &Registry{pipelines: make(map[string]*Pipeline)}
}

// Register adds a pipeline to the registry, keyed by its chain:network.
func (r *Registry) Register(p *Pipeline) {
	key := string(p.cfg.Chain) + ":" + string(p.cfg.Network)
	r.mu.Lock()
	r.pipelines[key] = p
	r.mu.Unlock()
}

// Get returns the pipeline for the given chain/network, or nil if not found.
func (r *Registry) Get(chain model.Chain, network model.Network) *Pipeline {
	key := string(chain) + ":" + string(network)
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pipelines[key]
}
