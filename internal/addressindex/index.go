package addressindex

import (
	"context"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// Index provides fast address membership testing and metadata lookup.
type Index interface {
	// Contains returns true if the address is (probably) a watched address.
	// A false return is definitive; a true return may require DB verification.
	Contains(ctx context.Context, chain model.Chain, network model.Network, address string) bool

	// Lookup returns the WatchedAddress if found, or nil if not watched.
	Lookup(ctx context.Context, chain model.Chain, network model.Network, address string) *model.WatchedAddress

	// Reload rebuilds the index from the database for the given chain/network.
	Reload(ctx context.Context, chain model.Chain, network model.Network) error
}
