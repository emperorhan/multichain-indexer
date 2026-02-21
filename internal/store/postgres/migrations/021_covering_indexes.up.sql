-- 1. Covering index for BulkGetAmountWithExistsTx tuple lookup
CREATE INDEX IF NOT EXISTS idx_balances_lookup
    ON balances (chain, network, address, token_id, balance_type)
    INCLUDE (amount, wallet_id, organization_id);

-- 2. Covering index for dashboard GetRecentEvents ORDER BY
CREATE INDEX IF NOT EXISTS idx_be_cursor_desc
    ON balance_events (chain, network, block_cursor DESC, created_at DESC);

-- 3. Composite index for dashboard address-filtered event queries
CREATE INDEX IF NOT EXISTS idx_be_address_cursor
    ON balance_events (chain, network, address, block_cursor DESC, created_at DESC);
