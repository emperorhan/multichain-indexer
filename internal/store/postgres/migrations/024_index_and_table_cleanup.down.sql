-- Restore reconciliation snapshot indexes
CREATE INDEX IF NOT EXISTS idx_recon_snapshots_mismatch ON balance_reconciliation_snapshots (chain, network, is_match) WHERE NOT is_match;
CREATE INDEX IF NOT EXISTS idx_recon_snapshots_checked_at ON balance_reconciliation_snapshots (checked_at);
CREATE INDEX IF NOT EXISTS idx_recon_snapshots_chain_network ON balance_reconciliation_snapshots (chain, network);

-- Restore original watched index and drop replacement
DROP INDEX IF EXISTS idx_be_watched_cursor;
CREATE INDEX IF NOT EXISTS idx_be_watched ON balance_events (chain, network, watched_address, block_time DESC);

-- Restore balance_events indexes
CREATE INDEX IF NOT EXISTS idx_be_wallet_activity ON balance_events (wallet_id, activity_type, block_time DESC);
CREATE INDEX IF NOT EXISTS idx_be_activity ON balance_events (chain, network, watched_address, activity_type, block_time DESC);
CREATE INDEX IF NOT EXISTS idx_be_token ON balance_events (token_id);
CREATE INDEX IF NOT EXISTS idx_be_wallet ON balance_events (wallet_id);
CREATE INDEX IF NOT EXISTS idx_be_address ON balance_events (address);

-- Restore balances index
CREATE INDEX IF NOT EXISTS idx_balances_wallet ON balances (wallet_id);

-- Restore transaction indexes
CREATE INDEX IF NOT EXISTS idx_tx_block_time ON transactions (block_time);
CREATE INDEX IF NOT EXISTS idx_tx_fee_payer ON transactions (fee_payer);

-- Note: indexer_configs table cannot be restored (data lost). Recreate structure only:
CREATE TABLE IF NOT EXISTS indexer_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain TEXT NOT NULL,
    network TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    target_batch_size INTEGER NOT NULL DEFAULT 100,
    indexing_interval_ms INTEGER NOT NULL DEFAULT 5000,
    chain_config JSONB,
    rpc_url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain, network)
);
