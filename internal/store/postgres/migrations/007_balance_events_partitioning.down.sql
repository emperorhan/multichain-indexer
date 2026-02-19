-- Rollback partitioning: swap back to original table.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'balance_events_old') THEN
        RAISE EXCEPTION 'Cannot rollback: balance_events_old table does not exist. It may have been manually dropped.';
    END IF;
END $$;

ALTER TABLE balance_events RENAME TO balance_events_partitioned_bak;
ALTER TABLE balance_events_old RENAME TO balance_events;
DROP TABLE IF EXISTS balance_events_partitioned_bak CASCADE;
