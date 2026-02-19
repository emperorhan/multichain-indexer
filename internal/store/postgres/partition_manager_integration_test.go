//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/store/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDBForPartition(t *testing.T) *postgres.DB {
	t.Helper()
	url := os.Getenv("TEST_DB_URL")
	if url != "" {
		db, err := postgres.New(postgres.Config{
			URL:             url,
			MaxOpenConns:    5,
			MaxIdleConns:    2,
			ConnMaxLifetime: time.Minute,
		})
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		return db
	}
	return setupTestContainer(t)
}

func TestPartitionManager_EnsurePartitions_ZeroDays(t *testing.T) {
	db := testDBForPartition(t)
	pm := postgres.NewPartitionManager(db)
	ctx := context.Background()

	// days=0 should create exactly one partition for today.
	err := pm.EnsurePartitions(ctx, 0)
	require.NoError(t, err)

	// Verify the partition exists by querying pg_class.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	partName := fmt.Sprintf("balance_events_%s", today.Format("2006_01_02"))

	var exists bool
	err = db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname = $1)",
		partName,
	).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists, "partition %s should exist after EnsurePartitions(ctx, 0)", partName)
}

func TestPartitionManager_EnsurePartitions_Idempotent(t *testing.T) {
	db := testDBForPartition(t)
	pm := postgres.NewPartitionManager(db)
	ctx := context.Background()

	// Calling twice should not error.
	require.NoError(t, pm.EnsurePartitions(ctx, 1))
	require.NoError(t, pm.EnsurePartitions(ctx, 1))
}

func TestPartitionManager_EnsurePartitions_MultipleDays(t *testing.T) {
	db := testDBForPartition(t)
	pm := postgres.NewPartitionManager(db)
	ctx := context.Background()

	days := 3
	require.NoError(t, pm.EnsurePartitions(ctx, days))

	today := time.Now().UTC().Truncate(24 * time.Hour)
	for d := 0; d <= days; d++ {
		date := today.AddDate(0, 0, d)
		partName := fmt.Sprintf("balance_events_%s", date.Format("2006_01_02"))

		var exists bool
		err := db.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname = $1)",
			partName,
		).Scan(&exists)
		require.NoError(t, err)
		assert.True(t, exists, "partition %s should exist", partName)
	}
}

func TestPartitionManager_DropOldPartitions_ZeroRetention(t *testing.T) {
	db := testDBForPartition(t)
	pm := postgres.NewPartitionManager(db)
	ctx := context.Background()

	// This should succeed even if there are no old partitions.
	dropped, err := pm.DropOldPartitions(ctx, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, dropped, 0, "dropped count should be non-negative")
}
