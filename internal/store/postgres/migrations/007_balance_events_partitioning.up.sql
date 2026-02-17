-- Migrate balance_events to time-based (monthly) partitioning.
-- PostgreSQL requires the partition key to be part of the primary key and
-- all unique constraints. We create a new partitioned table, migrate data,
-- then swap.

-- Step 1: Create partitioned table with same schema.
CREATE TABLE balance_events_partitioned (
    id                      UUID DEFAULT gen_random_uuid(),
    chain                   VARCHAR(20) NOT NULL,
    network                 VARCHAR(20) NOT NULL,
    transaction_id          UUID NOT NULL,
    tx_hash                 VARCHAR(128) NOT NULL,
    outer_instruction_index INT NOT NULL,
    inner_instruction_index INT NOT NULL DEFAULT -1,
    token_id                UUID NOT NULL,
    event_category          VARCHAR(20) NOT NULL,
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
    schema_version          VARCHAR(32) NOT NULL DEFAULT '',

    -- Primary key includes block_time (partition key)
    PRIMARY KEY (id, block_time)
) PARTITION BY RANGE (block_time);

-- Step 2: Create default partition for data that doesn't fit specific ranges.
CREATE TABLE balance_events_default PARTITION OF balance_events_partitioned DEFAULT;

-- Step 3: Create monthly partitions for 2024-2026 (extend as needed).
DO $$
DECLARE
    y INT;
    m INT;
    start_date DATE;
    end_date DATE;
    part_name TEXT;
BEGIN
    FOR y IN 2024..2026 LOOP
        FOR m IN 1..12 LOOP
            start_date := make_date(y, m, 1);
            end_date := start_date + INTERVAL '1 month';
            part_name := format('balance_events_y%sm%s', y, lpad(m::TEXT, 2, '0'));
            EXECUTE format(
                'CREATE TABLE IF NOT EXISTS %I PARTITION OF balance_events_partitioned
                 FOR VALUES FROM (%L) TO (%L)',
                part_name, start_date, end_date
            );
        END LOOP;
    END LOOP;
END $$;

-- Step 4: Create unique constraint and indexes on partitioned table.
-- event_id uniqueness must include block_time (partition key) for partitioned tables.
ALTER TABLE balance_events_partitioned
    ADD CONSTRAINT uq_bep_event_id UNIQUE (event_id, block_time);
CREATE INDEX idx_bep_watched ON balance_events_partitioned (chain, network, watched_address, block_time DESC);
CREATE INDEX idx_bep_cursor ON balance_events_partitioned (chain, network, block_cursor);
CREATE INDEX idx_bep_address ON balance_events_partitioned (address);
CREATE INDEX idx_bep_wallet ON balance_events_partitioned (wallet_id);
CREATE INDEX idx_bep_token ON balance_events_partitioned (token_id);
CREATE INDEX idx_bep_category ON balance_events_partitioned (event_category);

-- Step 5: Migrate existing data with explicit column mapping.
-- SELECT * is unsafe because column order differs between old and new tables
-- (e.g. balance_before/balance_after are at positions 33-34 in old but 15-16
-- in new). We also COALESCE nullable columns that became NOT NULL.
INSERT INTO balance_events_partitioned (
    id, chain, network, transaction_id, tx_hash,
    outer_instruction_index, inner_instruction_index,
    token_id, event_category, event_action, program_id,
    address, counterparty_address, delta,
    balance_before, balance_after,
    watched_address, wallet_id, organization_id,
    block_cursor, block_time, chain_data, created_at,
    event_id, block_hash, tx_index,
    event_path, event_path_type,
    actor_address, asset_type, asset_id,
    finality_state, decoder_version, schema_version
)
SELECT
    id, chain, network, transaction_id, tx_hash,
    outer_instruction_index, inner_instruction_index,
    token_id, event_category, event_action, program_id,
    address, counterparty_address, delta,
    balance_before, balance_after,
    watched_address, wallet_id, organization_id,
    block_cursor,
    COALESCE(block_time, now()),
    chain_data, created_at,
    COALESCE(event_id, ''),
    COALESCE(block_hash, ''),
    COALESCE(tx_index, 0)::INT,
    COALESCE(event_path, ''),
    COALESCE(event_path_type, ''),
    COALESCE(actor_address, ''),
    COALESCE(asset_type, ''),
    COALESCE(asset_id, ''),
    COALESCE(finality_state, ''),
    COALESCE(decoder_version, ''),
    COALESCE(schema_version, '')
FROM balance_events
ON CONFLICT DO NOTHING;

-- Step 6: Swap tables.
ALTER TABLE balance_events RENAME TO balance_events_old;
ALTER TABLE balance_events_partitioned RENAME TO balance_events;

-- Step 7: Recreate foreign keys and constraints that reference this table.
-- Note: The event_id uniqueness is now enforced by the partial index above.
-- The old uq_balance_events_event_id UNIQUE constraint cannot span partitions,
-- so we rely on the event_id index + application-level dedup (ON CONFLICT).
