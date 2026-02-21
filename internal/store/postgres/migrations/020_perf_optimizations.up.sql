-- 1. Create finality_rank() immutable function for reuse across queries
CREATE OR REPLACE FUNCTION finality_rank(state VARCHAR) RETURNS INT AS $$
SELECT CASE LOWER(COALESCE(TRIM(state), ''))
    WHEN 'finalized' THEN 4
    WHEN 'safe' THEN 3
    WHEN 'confirmed' THEN 2
    WHEN 'pending' THEN 1
    ELSE 0
END;
$$ LANGUAGE SQL IMMUTABLE;

-- 2. Drop redundant indexes superseded by migration 018 composite indexes
DROP INDEX IF EXISTS idx_balance_events_activity;
DROP INDEX IF EXISTS idx_balance_events_address;
DROP INDEX IF EXISTS idx_balance_events_wallet;

-- 3. Add covering index for watched_addresses ORDER BY created_at
DROP INDEX IF EXISTS idx_watched_addr_active;
CREATE INDEX idx_watched_addr_active ON watched_addresses (chain, network, created_at) WHERE is_active = true;
