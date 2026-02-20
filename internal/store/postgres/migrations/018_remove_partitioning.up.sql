-- Migration 018: Remove daily partitioning from balance_events.
-- Converts from PARTITION BY RANGE (block_time) to a flat table with
-- PK(id) and UNIQUE(event_id), eliminating canonical_ids lookup table.

-- Step 1: Create flat (non-partitioned) table with identical columns.
CREATE TABLE balance_events_flat (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain                   VARCHAR(20) NOT NULL,
    network                 VARCHAR(20) NOT NULL,
    transaction_id          UUID NOT NULL,
    tx_hash                 VARCHAR(128) NOT NULL,
    outer_instruction_index INT NOT NULL,
    inner_instruction_index INT NOT NULL DEFAULT -1,
    token_id                UUID NOT NULL,
    activity_type           VARCHAR(40) NOT NULL,
    event_action            VARCHAR(60) NOT NULL,
    program_id              VARCHAR(128) NOT NULL,
    address                 VARCHAR(128) NOT NULL,
    counterparty_address    VARCHAR(128) NOT NULL DEFAULT '',
    delta                   NUMERIC(78,0) NOT NULL,
    balance_before          NUMERIC(78,0),
    balance_after           NUMERIC(78,0),
    watched_address         VARCHAR(128),
    wallet_id               VARCHAR(100),
    organization_id         VARCHAR(100),
    block_cursor            BIGINT NOT NULL,
    block_time              TIMESTAMPTZ NOT NULL DEFAULT now(),
    chain_data              JSONB NOT NULL DEFAULT '{}',
    balance_applied         BOOLEAN NOT NULL DEFAULT false,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    event_id                VARCHAR(512) NOT NULL DEFAULT '',
    block_hash              VARCHAR(128) NOT NULL DEFAULT '',
    tx_index                INT NOT NULL DEFAULT 0,
    event_path              VARCHAR(512) NOT NULL DEFAULT '',
    event_path_type         VARCHAR(64) NOT NULL DEFAULT '',
    actor_address           VARCHAR(128) NOT NULL DEFAULT '',
    asset_type              VARCHAR(64) NOT NULL DEFAULT '',
    asset_id                VARCHAR(256) NOT NULL DEFAULT '',
    finality_state          VARCHAR(32) NOT NULL DEFAULT '',
    decoder_version         VARCHAR(64) NOT NULL DEFAULT '',
    schema_version          VARCHAR(32) NOT NULL DEFAULT ''
);

-- Step 2: Migrate data with dedup by event_id (keep newest per event_id).
INSERT INTO balance_events_flat (
    id, chain, network, transaction_id, tx_hash,
    outer_instruction_index, inner_instruction_index,
    token_id, activity_type, event_action, program_id,
    address, counterparty_address, delta,
    balance_before, balance_after,
    watched_address, wallet_id, organization_id,
    block_cursor, block_time, chain_data, balance_applied, created_at,
    event_id, block_hash, tx_index,
    event_path, event_path_type,
    actor_address, asset_type, asset_id,
    finality_state, decoder_version, schema_version
)
SELECT
    id, chain, network, transaction_id, tx_hash,
    outer_instruction_index, inner_instruction_index,
    token_id, activity_type, event_action, program_id,
    address, counterparty_address, delta,
    balance_before, balance_after,
    watched_address, wallet_id, organization_id,
    block_cursor, block_time, chain_data, balance_applied, created_at,
    event_id, block_hash, tx_index,
    event_path, event_path_type,
    actor_address, asset_type, asset_id,
    finality_state, decoder_version, schema_version
FROM (
    SELECT *,
        ROW_NUMBER() OVER (
            PARTITION BY event_id
            ORDER BY created_at DESC, block_time DESC, id
        ) AS rn
    FROM balance_events
    WHERE event_id <> ''
) deduped
WHERE rn = 1
ON CONFLICT DO NOTHING;

-- Also migrate rows with empty event_id (legacy data).
INSERT INTO balance_events_flat (
    id, chain, network, transaction_id, tx_hash,
    outer_instruction_index, inner_instruction_index,
    token_id, activity_type, event_action, program_id,
    address, counterparty_address, delta,
    balance_before, balance_after,
    watched_address, wallet_id, organization_id,
    block_cursor, block_time, chain_data, balance_applied, created_at,
    event_id, block_hash, tx_index,
    event_path, event_path_type,
    actor_address, asset_type, asset_id,
    finality_state, decoder_version, schema_version
)
SELECT
    id, chain, network, transaction_id, tx_hash,
    outer_instruction_index, inner_instruction_index,
    token_id, activity_type, event_action, program_id,
    address, counterparty_address, delta,
    balance_before, balance_after,
    watched_address, wallet_id, organization_id,
    block_cursor, block_time, chain_data, balance_applied, created_at,
    event_id, block_hash, tx_index,
    event_path, event_path_type,
    actor_address, asset_type, asset_id,
    finality_state, decoder_version, schema_version
FROM balance_events
WHERE event_id = '' OR event_id IS NULL
ON CONFLICT DO NOTHING;

-- Step 3: Swap tables.
ALTER TABLE balance_events RENAME TO balance_events_partitioned_old;
ALTER TABLE balance_events_flat RENAME TO balance_events;

-- Step 4: Create unique constraint on event_id (non-empty only).
-- Drop legacy constraint from migration 005 (lives on balance_events_old after 007 rename).
-- Must use DROP CONSTRAINT because it was created via ALTER TABLE ADD CONSTRAINT.
ALTER TABLE IF EXISTS balance_events_old DROP CONSTRAINT IF EXISTS uq_balance_events_event_id;
CREATE UNIQUE INDEX uq_balance_events_event_id
    ON balance_events (event_id)
    WHERE event_id <> '';

-- Step 5: Recreate indexes on the flat table.
CREATE INDEX idx_be_watched ON balance_events (chain, network, watched_address, block_time DESC);
CREATE INDEX idx_be_cursor ON balance_events (chain, network, block_cursor);
CREATE INDEX idx_be_address ON balance_events (address);
CREATE INDEX idx_be_wallet ON balance_events (wallet_id);
CREATE INDEX idx_be_token ON balance_events (token_id);
CREATE INDEX idx_be_activity ON balance_events (chain, network, watched_address, activity_type, block_time DESC);
CREATE INDEX idx_be_wallet_activity ON balance_events (wallet_id, activity_type, block_time DESC);
CREATE INDEX idx_be_finality ON balance_events (chain, network, block_cursor, finality_state)
    WHERE finality_state <> '' AND finality_state <> 'finalized';

-- Step 6: Drop the canonical_ids lookup table (no longer needed).
DROP TABLE IF EXISTS balance_event_canonical_ids;

-- Step 7: Drop partition manager functions.
DROP FUNCTION IF EXISTS create_daily_partitions(INT);
DROP FUNCTION IF EXISTS reroute_default_partition();
DROP FUNCTION IF EXISTS drop_old_daily_partitions(INT);
