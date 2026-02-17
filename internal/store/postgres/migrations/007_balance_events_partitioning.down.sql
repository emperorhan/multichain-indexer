-- Rollback partitioning: swap back to original table.
ALTER TABLE balance_events RENAME TO balance_events_partitioned_bak;
ALTER TABLE balance_events_old RENAME TO balance_events;
DROP TABLE IF EXISTS balance_events_partitioned_bak CASCADE;
