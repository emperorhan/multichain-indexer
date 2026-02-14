DROP INDEX IF EXISTS idx_balance_events_balance_after;
DROP INDEX IF EXISTS idx_balance_events_balance_before;

ALTER TABLE balance_events
    DROP COLUMN IF EXISTS balance_after,
    DROP COLUMN IF EXISTS balance_before;
