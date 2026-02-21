-- Reverse covering index
DROP INDEX IF EXISTS idx_watched_addr_active;
CREATE INDEX idx_watched_addr_active ON watched_addresses (chain, network) WHERE is_active = true;

-- Restore standalone indexes
CREATE INDEX IF NOT EXISTS idx_balance_events_wallet ON balance_events (wallet_id);
CREATE INDEX IF NOT EXISTS idx_balance_events_address ON balance_events (address);
CREATE INDEX IF NOT EXISTS idx_balance_events_activity ON balance_events (activity_type);

-- Drop finality_rank function
DROP FUNCTION IF EXISTS finality_rank(VARCHAR);
