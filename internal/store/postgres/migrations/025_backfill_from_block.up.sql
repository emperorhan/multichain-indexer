ALTER TABLE watched_addresses ADD COLUMN backfill_from_block BIGINT NULL;

COMMENT ON COLUMN watched_addresses.backfill_from_block IS
    'Block number to backfill from for newly added addresses. NULL = no pending backfill.';

CREATE INDEX idx_wa_pending_backfill ON watched_addresses (chain, network)
    WHERE backfill_from_block IS NOT NULL AND is_active = true;
