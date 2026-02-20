-- Add partial index on balance_applied = false for finality promotion queries.
CREATE INDEX IF NOT EXISTS idx_be_unapplied
    ON balance_events (chain, network, block_cursor)
    WHERE balance_applied = false;
