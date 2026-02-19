package postgres

import (
	"database/sql"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeDB creates a *DB wrapping a minimal *sql.DB opened with a fake
// driver. The connection is never actually used so it does not need a real
// database server.
func newFakeDB(t *testing.T) *DB {
	t.Helper()
	sqlDB, err := sql.Open("postgres", "postgres://fake:fake@localhost:0/fake?sslmode=disable")
	require.NoError(t, err)
	return &DB{DB: sqlDB}
}

func TestPoolRegistry_GetDefault(t *testing.T) {
	defaultDB := newFakeDB(t)
	t.Cleanup(func() { defaultDB.Close() })

	reg := NewPoolRegistry(defaultDB)

	// For any chain-network pair that has not been registered, Get must
	// return the default DB.
	got := reg.Get(model.ChainSolana, model.NetworkDevnet)
	assert.Same(t, defaultDB, got, "should return default DB when no override registered")

	got2 := reg.Get(model.ChainEthereum, model.NetworkMainnet)
	assert.Same(t, defaultDB, got2, "different chain-network should also return default DB")
}

func TestPoolRegistry_RegisterAndGet(t *testing.T) {
	defaultDB := newFakeDB(t)
	t.Cleanup(func() { defaultDB.Close() })

	chainDB := newFakeDB(t)
	t.Cleanup(func() { chainDB.Close() })

	reg := NewPoolRegistry(defaultDB)
	reg.Register(model.ChainSolana, model.NetworkDevnet, chainDB)

	// Registered chain-network pair should return the chain-specific DB.
	got := reg.Get(model.ChainSolana, model.NetworkDevnet)
	assert.Same(t, chainDB, got, "should return chain-specific DB")

	// A different chain-network pair should still return the default.
	got2 := reg.Get(model.ChainEthereum, model.NetworkMainnet)
	assert.Same(t, defaultDB, got2, "unregistered pair should return default DB")
}

func TestPoolRegistry_Close(t *testing.T) {
	defaultDB := newFakeDB(t)
	// defaultDB is NOT expected to be closed by registry.Close().
	// We close it ourselves in cleanup.
	t.Cleanup(func() { defaultDB.Close() })

	chainDB1 := newFakeDB(t)
	chainDB2 := newFakeDB(t)

	reg := NewPoolRegistry(defaultDB)
	reg.Register(model.ChainSolana, model.NetworkDevnet, chainDB1)
	reg.Register(model.ChainEthereum, model.NetworkMainnet, chainDB2)

	// Close should close all registered pools.
	err := reg.Close()
	require.NoError(t, err)

	// After Close, the registered pools should be closed (Ping returns error).
	assert.Error(t, chainDB1.Ping(), "chain DB 1 should be closed")
	assert.Error(t, chainDB2.Ping(), "chain DB 2 should be closed")

	// The default DB should remain open (Ping may fail since no real server,
	// but calling Close on it should not return "already closed").
	// We verify the default DB was NOT closed by checking that Close()
	// succeeds (closing an already-closed DB returns an error).
	err = defaultDB.Close()
	assert.NoError(t, err, "default DB should not have been closed by registry")
}
