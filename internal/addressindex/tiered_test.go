package addressindex

import (
	"context"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockWatchedAddrRepo implements store.WatchedAddressRepository for testing.
type mockWatchedAddrRepo struct {
	active  []model.WatchedAddress
	byAddr  map[string]*model.WatchedAddress
	findErr error
}

func (m *mockWatchedAddrRepo) GetActive(_ context.Context, _ model.Chain, _ model.Network) ([]model.WatchedAddress, error) {
	return m.active, nil
}

func (m *mockWatchedAddrRepo) Upsert(_ context.Context, _ *model.WatchedAddress) error {
	return nil
}

func (m *mockWatchedAddrRepo) FindByAddress(_ context.Context, _ model.Chain, _ model.Network, address string) (*model.WatchedAddress, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	wa, ok := m.byAddr[address]
	if !ok {
		return nil, nil
	}
	return wa, nil
}

func newTestIndex(repo *mockWatchedAddrRepo) *TieredIndex {
	return NewTieredIndex(repo, TieredIndexConfig{
		BloomExpectedItems: 1000,
		BloomFPR:           0.001,
		LRUCapacity:        100,
		LRUTTL:             10 * time.Minute,
	})
}

func TestTieredIndex_BloomReject(t *testing.T) {
	repo := &mockWatchedAddrRepo{
		byAddr: map[string]*model.WatchedAddress{},
	}
	idx := newTestIndex(repo)

	// Never added to bloom, so should be rejected immediately
	result := idx.Contains(context.Background(), model.ChainSolana, "devnet", "unknown_addr")
	assert.False(t, result)

	wa := idx.Lookup(context.Background(), model.ChainSolana, "devnet", "unknown_addr")
	assert.Nil(t, wa)
}

func TestTieredIndex_LRUHit(t *testing.T) {
	walletID := "wallet1"
	wa := &model.WatchedAddress{
		Address:  "addr1",
		WalletID: &walletID,
		IsActive: true,
	}

	repo := &mockWatchedAddrRepo{
		active: []model.WatchedAddress{*wa},
		byAddr: map[string]*model.WatchedAddress{"addr1": wa},
	}
	idx := newTestIndex(repo)

	// Reload to populate bloom + LRU
	err := idx.Reload(context.Background(), model.ChainSolana, "devnet")
	require.NoError(t, err)

	// Should hit LRU (not DB) — the repo data doesn't matter anymore
	result := idx.Lookup(context.Background(), model.ChainSolana, "devnet", "addr1")
	require.NotNil(t, result)
	assert.Equal(t, "addr1", result.Address)
	assert.Equal(t, &walletID, result.WalletID)
}

func TestTieredIndex_DBFallback(t *testing.T) {
	walletID := "wallet1"
	wa := &model.WatchedAddress{
		Address:  "addr1",
		WalletID: &walletID,
		IsActive: true,
	}

	repo := &mockWatchedAddrRepo{
		active: []model.WatchedAddress{*wa},
		byAddr: map[string]*model.WatchedAddress{"addr1": wa},
	}
	idx := newTestIndex(repo)

	// Add to bloom (simulates Reload adding bloom entries) but don't put in LRU
	key := lruKey(model.ChainSolana, "devnet", "addr1")
	bf := NewBloomFilter(1000, 0.001)
	bf.Add(key)
	idx.blooms[bloomKey(model.ChainSolana, "devnet")] = bf

	// Bloom says "maybe" → LRU miss → falls through to DB
	result := idx.Lookup(context.Background(), model.ChainSolana, "devnet", "addr1")
	require.NotNil(t, result)
	assert.Equal(t, "addr1", result.Address)

	// Second lookup should hit LRU now
	result2 := idx.Lookup(context.Background(), model.ChainSolana, "devnet", "addr1")
	require.NotNil(t, result2)
	assert.Equal(t, "addr1", result2.Address)
}

func TestTieredIndex_NegativeCaching(t *testing.T) {
	repo := &mockWatchedAddrRepo{
		byAddr: map[string]*model.WatchedAddress{},
	}
	idx := newTestIndex(repo)

	// Add to bloom but not in DB
	key := lruKey(model.ChainSolana, "devnet", "not_watched")
	bf := NewBloomFilter(1000, 0.001)
	bf.Add(key)
	idx.blooms[bloomKey(model.ChainSolana, "devnet")] = bf

	// Bloom says "maybe" → LRU miss → DB returns nil → negative cache
	result := idx.Lookup(context.Background(), model.ChainSolana, "devnet", "not_watched")
	assert.Nil(t, result)

	contains := idx.Contains(context.Background(), model.ChainSolana, "devnet", "not_watched")
	assert.False(t, contains)
}

func TestTieredIndex_Reload(t *testing.T) {
	wa1 := model.WatchedAddress{Address: "addr1", IsActive: true}
	wa2 := model.WatchedAddress{Address: "addr2", IsActive: true}

	repo := &mockWatchedAddrRepo{
		active: []model.WatchedAddress{wa1, wa2},
		byAddr: map[string]*model.WatchedAddress{
			"addr1": &wa1,
			"addr2": &wa2,
		},
	}
	idx := newTestIndex(repo)

	err := idx.Reload(context.Background(), model.ChainSolana, "devnet")
	require.NoError(t, err)

	// Both addresses should be in bloom after reload
	assert.True(t, idx.Contains(context.Background(), model.ChainSolana, "devnet", "addr1"))
	assert.True(t, idx.Contains(context.Background(), model.ChainSolana, "devnet", "addr2"))
	// Unknown address should not be in bloom
	assert.False(t, idx.Contains(context.Background(), model.ChainSolana, "devnet", "addr3"))
}
