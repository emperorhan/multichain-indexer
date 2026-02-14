-- Add canonical identity scaffolding to balance_events

ALTER TABLE balance_events
    ADD COLUMN event_id VARCHAR(255),
    ADD COLUMN block_hash VARCHAR(128),
    ADD COLUMN tx_index BIGINT,
    ADD COLUMN event_path TEXT,
    ADD COLUMN event_path_type VARCHAR(64),
    ADD COLUMN actor_address VARCHAR(128),
    ADD COLUMN asset_type VARCHAR(64),
    ADD COLUMN asset_id VARCHAR(128),
    ADD COLUMN finality_state VARCHAR(32),
    ADD COLUMN decoder_version VARCHAR(64),
    ADD COLUMN schema_version VARCHAR(32);

CREATE INDEX idx_balance_events_event_id
    ON balance_events (event_id);
CREATE INDEX idx_balance_events_event_path
    ON balance_events (event_path);
CREATE INDEX idx_balance_events_actor_address
    ON balance_events (actor_address);
CREATE INDEX idx_balance_events_asset_id
    ON balance_events (asset_id);
