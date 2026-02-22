-- Migration 024: DB Re-Audit cleanup (DB-RA-01 through DB-RA-05)

-- DB-RA-01: Drop dead indexer_configs table (no production read/write paths)
DROP TABLE IF EXISTS indexer_configs CASCADE;

-- DB-RA-02: Drop unused transaction indexes (no fee_payer/block_time queries exist)
DROP INDEX IF EXISTS idx_tx_fee_payer;
DROP INDEX IF EXISTS idx_tx_block_time;

-- DB-RA-03: Drop unused balances index (no wallet_id WHERE queries exist)
DROP INDEX IF EXISTS idx_balances_wallet;

-- DB-RA-04: Drop unused balance_events indexes (no active query paths)
DROP INDEX IF EXISTS idx_be_address;
DROP INDEX IF EXISTS idx_be_wallet;
DROP INDEX IF EXISTS idx_be_token;
DROP INDEX IF EXISTS idx_be_activity;
DROP INDEX IF EXISTS idx_be_wallet_activity;

-- DB-RA-04: Replace block_time-sorted watched index with block_cursor-sorted
-- Rollback queries filter on (chain, network, watched_address, block_cursor),
-- but idx_be_watched sorts by block_time DESC which is a column mismatch.
DROP INDEX IF EXISTS idx_be_watched;
CREATE INDEX idx_be_watched_cursor ON balance_events (chain, network, watched_address, block_cursor);

-- DB-RA-05: Drop unused reconciliation snapshot indexes (table is INSERT-only, zero SELECT queries)
DROP INDEX IF EXISTS idx_recon_snapshots_chain_network;
DROP INDEX IF EXISTS idx_recon_snapshots_checked_at;
DROP INDEX IF EXISTS idx_recon_snapshots_mismatch;
