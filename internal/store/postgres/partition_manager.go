package postgres

import (
	"context"
	"fmt"
	"time"
)

// PartitionManager manages daily partitions for the balance_events table.
// It creates partitions using the naming convention balance_events_YYYY_MM_DD.
// All operations are idempotent and safe to call concurrently.
type PartitionManager struct {
	db *DB
}

// NewPartitionManager returns a new PartitionManager that operates on the given DB.
func NewPartitionManager(db *DB) *PartitionManager {
	return &PartitionManager{db: db}
}

// EnsurePartitions creates daily partitions for balance_events covering
// the next `days` days starting from today (UTC). Each partition covers
// exactly one day: [YYYY-MM-DD 00:00, YYYY-MM-DD+1 00:00).
//
// The method is idempotent: it uses CREATE TABLE IF NOT EXISTS internally
// and catches duplicate_table errors, making it safe to call concurrently
// from multiple processes.
func (pm *PartitionManager) EnsurePartitions(ctx context.Context, days int) error {
	if days < 0 {
		return fmt.Errorf("partition manager: days must be non-negative, got %d", days)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)

	for d := 0; d <= days; d++ {
		startDate := today.AddDate(0, 0, d)
		endDate := startDate.AddDate(0, 0, 1)

		partName := fmt.Sprintf("balance_events_%s", startDate.Format("2006_01_02"))
		startStr := startDate.Format("2006-01-02")
		endStr := endDate.Format("2006-01-02")

		// Use a DO block with exception handling so that concurrent callers
		// do not fail if the partition is created between the IF NOT EXISTS
		// check and the actual CREATE. This is more robust than plain
		// CREATE TABLE IF NOT EXISTS for partitions, which can raise
		// duplicate_object when the range overlaps an existing partition.
		query := fmt.Sprintf(`
			DO $$
			BEGIN
				CREATE TABLE IF NOT EXISTS %s PARTITION OF balance_events
					FOR VALUES FROM ('%s') TO ('%s');
			EXCEPTION
				WHEN duplicate_table THEN NULL;
				WHEN duplicate_object THEN NULL;
			END $$;
		`, quoteIdent(partName), startStr, endStr)

		if _, err := pm.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("partition manager: create partition %s: %w", partName, err)
		}
	}

	return nil
}

// DropOldPartitions detaches and drops daily partitions older than the
// given retention period. Returns the number of partitions dropped.
func (pm *PartitionManager) DropOldPartitions(ctx context.Context, retentionDays int) (int, error) {
	if retentionDays < 0 {
		return 0, fmt.Errorf("partition manager: retentionDays must be non-negative, got %d", retentionDays)
	}

	query := `SELECT drop_old_daily_partitions($1)`
	var dropped int
	if err := pm.db.QueryRowContext(ctx, query, retentionDays).Scan(&dropped); err != nil {
		return 0, fmt.Errorf("partition manager: drop old partitions: %w", err)
	}
	return dropped, nil
}

// quoteIdent quotes a SQL identifier to prevent injection.
// This is a minimal implementation for partition names that follow the
// pattern balance_events_YYYY_MM_DD (alphanumeric + underscores only).
func quoteIdent(name string) string {
	// Use double-quoting as per SQL standard.
	escaped := ""
	for _, c := range name {
		if c == '"' {
			escaped += `""`
		} else {
			escaped += string(c)
		}
	}
	return `"` + escaped + `"`
}
