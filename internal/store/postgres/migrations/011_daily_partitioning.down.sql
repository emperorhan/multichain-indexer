-- Rollback daily partitioning: swap back to the monthly-partitioned table.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'balance_events_monthly_old') THEN
        RAISE EXCEPTION 'Cannot rollback: balance_events_monthly_old table does not exist. It may have been manually dropped.';
    END IF;
END $$;

-- Step 1: Rename daily-partitioned table away.
ALTER TABLE balance_events RENAME TO balance_events_daily_bak;

-- Step 2: Restore the monthly-partitioned table.
ALTER TABLE balance_events_monthly_old RENAME TO balance_events;

-- Step 3: Drop the daily-partitioned backup and all its partitions.
DROP TABLE IF EXISTS balance_events_daily_bak CASCADE;

-- Step 4: Drop the partition manager functions.
DROP FUNCTION IF EXISTS create_daily_partitions(INT);
DROP FUNCTION IF EXISTS drop_old_daily_partitions(INT);
DROP FUNCTION IF EXISTS reroute_default_partition();
