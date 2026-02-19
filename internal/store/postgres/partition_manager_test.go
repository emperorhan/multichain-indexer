package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// quoteIdent — pure function, no DB required
// ---------------------------------------------------------------------------

func TestQuoteIdent_NormalName(t *testing.T) {
	got := quoteIdent("balance_events_2025_01_01")
	assert.Equal(t, `"balance_events_2025_01_01"`, got)
}

func TestQuoteIdent_NameWithDoubleQuotes(t *testing.T) {
	// Double quotes inside the identifier must be escaped by doubling them.
	got := quoteIdent(`balance"events`)
	assert.Equal(t, `"balance""events"`, got)
}

func TestQuoteIdent_EmptyName(t *testing.T) {
	got := quoteIdent("")
	assert.Equal(t, `""`, got)
}

func TestQuoteIdent_MultipleDoubleQuotes(t *testing.T) {
	got := quoteIdent(`a""b`)
	assert.Equal(t, `"a""""b"`, got)
}

func TestQuoteIdent_OnlyDoubleQuotes(t *testing.T) {
	got := quoteIdent(`""`)
	assert.Equal(t, `""""""`, got)
}

// ---------------------------------------------------------------------------
// NewPartitionManager — constructor sanity check
// ---------------------------------------------------------------------------

func TestNewPartitionManager(t *testing.T) {
	pm := NewPartitionManager(nil)
	require.NotNil(t, pm, "NewPartitionManager should return a non-nil value even with a nil DB")
}

func TestNewPartitionManager_WithDB(t *testing.T) {
	db := &DB{} // zero-value, not connected
	pm := NewPartitionManager(db)
	require.NotNil(t, pm)
	assert.Equal(t, db, pm.db, "PartitionManager should hold the provided DB reference")
}

// ---------------------------------------------------------------------------
// EnsurePartitions — negative days validation (no DB needed)
// ---------------------------------------------------------------------------

func TestEnsurePartitions_NegativeDays(t *testing.T) {
	pm := NewPartitionManager(nil) // nil DB is fine; we expect early return
	err := pm.EnsurePartitions(context.Background(), -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "days must be non-negative")
	assert.Contains(t, err.Error(), "-1")
}

func TestEnsurePartitions_NegativeLargeDays(t *testing.T) {
	pm := NewPartitionManager(nil)
	err := pm.EnsurePartitions(context.Background(), -100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "days must be non-negative")
	assert.Contains(t, err.Error(), "-100")
}

// ---------------------------------------------------------------------------
// DropOldPartitions — negative retentionDays validation (no DB needed)
// ---------------------------------------------------------------------------

func TestDropOldPartitions_NegativeRetentionDays(t *testing.T) {
	pm := NewPartitionManager(nil)
	dropped, err := pm.DropOldPartitions(context.Background(), -1)
	require.Error(t, err)
	assert.Equal(t, 0, dropped)
	assert.Contains(t, err.Error(), "retentionDays must be non-negative")
	assert.Contains(t, err.Error(), "-1")
}
