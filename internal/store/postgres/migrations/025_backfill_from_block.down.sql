DROP INDEX IF EXISTS idx_wa_pending_backfill;
ALTER TABLE watched_addresses DROP COLUMN IF EXISTS backfill_from_block;
