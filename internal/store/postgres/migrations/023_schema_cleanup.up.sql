-- Migration 023: Schema cleanup — drop dead schema elements identified in DB audit
-- See docs/audit/ for full audit details

BEGIN;

-- DB-AUD-01: Drop legacy tables from migration history (007, 011, 018 renames)
DROP TABLE IF EXISTS balance_events_old CASCADE;
DROP TABLE IF EXISTS balance_events_monthly_old CASCADE;
DROP TABLE IF EXISTS balance_events_monthly_old_default CASCADE;
DROP TABLE IF EXISTS balance_events_partitioned_old CASCADE;

-- DB-AUD-02: head_sequence is never written (always DEFAULT 0)
ALTER TABLE pipeline_watermarks DROP COLUMN IF EXISTS head_sequence;

-- DB-AUD-03: rpc_url is never written or read by application code
ALTER TABLE indexer_configs DROP COLUMN IF EXISTS rpc_url;

-- DB-AUD-04: pending_withdrawal_amount has no write path (always DEFAULT 0)
ALTER TABLE balances DROP COLUMN IF EXISTS pending_withdrawal_amount;

-- DB-AUD-05: idx_runtime_configs_lookup is redundant with idx_runtime_config_active (partial index)
DROP INDEX IF EXISTS idx_runtime_configs_lookup;

-- DB-AUD-06: idx_indexed_blocks_cursor is redundant with PK backward scan
DROP INDEX IF EXISTS idx_indexed_blocks_cursor;

COMMIT;
