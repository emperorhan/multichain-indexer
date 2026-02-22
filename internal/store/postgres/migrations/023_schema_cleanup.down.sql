-- Migration 023 rollback: restore dropped schema elements

BEGIN;

-- Restore indexes
CREATE INDEX IF NOT EXISTS idx_indexed_blocks_cursor ON indexed_blocks (chain, network, block_number DESC);
CREATE INDEX IF NOT EXISTS idx_runtime_configs_lookup ON runtime_configs (chain, network);

-- Restore columns
ALTER TABLE balances ADD COLUMN IF NOT EXISTS pending_withdrawal_amount NUMERIC(78,0) NOT NULL DEFAULT 0;
ALTER TABLE indexer_configs ADD COLUMN IF NOT EXISTS rpc_url TEXT NOT NULL DEFAULT '';
ALTER TABLE pipeline_watermarks ADD COLUMN IF NOT EXISTS head_sequence BIGINT NOT NULL DEFAULT 0;

-- Note: _old tables cannot be restored (data was in them before migration 023 dropped them)
-- If rollback is needed, restore from backup

COMMIT;
