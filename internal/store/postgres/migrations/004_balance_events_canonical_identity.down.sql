DROP INDEX IF EXISTS idx_balance_events_event_id;
DROP INDEX IF EXISTS idx_balance_events_event_path;
DROP INDEX IF EXISTS idx_balance_events_actor_address;
DROP INDEX IF EXISTS idx_balance_events_asset_id;

ALTER TABLE balance_events
    DROP COLUMN IF EXISTS schema_version,
    DROP COLUMN IF EXISTS decoder_version,
    DROP COLUMN IF EXISTS finality_state,
    DROP COLUMN IF EXISTS asset_id,
    DROP COLUMN IF EXISTS asset_type,
    DROP COLUMN IF EXISTS actor_address,
    DROP COLUMN IF EXISTS event_path_type,
    DROP COLUMN IF EXISTS event_path,
    DROP COLUMN IF EXISTS tx_index,
    DROP COLUMN IF EXISTS block_hash,
    DROP COLUMN IF EXISTS event_id;
