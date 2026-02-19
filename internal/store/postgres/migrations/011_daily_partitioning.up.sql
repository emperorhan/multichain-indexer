-- Migrate balance_events from monthly partitioning to daily partitioning.
-- The table is already PARTITION BY RANGE (block_time) from migration 007.
-- Strategy:
--   1. Create a new daily-partitioned table with the same schema
--   2. Create daily partitions for today + next 7 days
--   3. Migrate all existing data (from monthly partitions)
--   4. Swap tables
--   5. Create a partition manager function for auto-creating future partitions

-- Step 1: Create daily-partitioned table with identical schema.
CREATE TABLE balance_events_daily (
    id                      UUID DEFAULT gen_random_uuid(),
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
    schema_version          VARCHAR(32) NOT NULL DEFAULT '',

    PRIMARY KEY (id, block_time)
) PARTITION BY RANGE (block_time);

-- Step 2: Create a default partition for data outside explicit daily ranges.
CREATE TABLE balance_events_daily_default PARTITION OF balance_events_daily DEFAULT;

-- Step 3: Create daily partitions for today + next 7 days.
DO $$
DECLARE
    d INT;
    start_date DATE;
    end_date DATE;
    part_name TEXT;
BEGIN
    FOR d IN 0..7 LOOP
        start_date := CURRENT_DATE + d;
        end_date := start_date + INTERVAL '1 day';
        part_name := 'balance_events_' || to_char(start_date, 'YYYY_MM_DD');
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF balance_events_daily
             FOR VALUES FROM (%L) TO (%L)',
            part_name, start_date, end_date
        );
    END LOOP;
END $$;

-- Step 4: Recreate indexes and constraints on the new partitioned table.
ALTER TABLE balance_events_daily
    ADD CONSTRAINT uq_bed_event_id UNIQUE (event_id, block_time);
CREATE INDEX idx_bed_watched ON balance_events_daily (chain, network, watched_address, block_time DESC);
CREATE INDEX idx_bed_cursor ON balance_events_daily (chain, network, block_cursor);
CREATE INDEX idx_bed_address ON balance_events_daily (address);
CREATE INDEX idx_bed_wallet ON balance_events_daily (wallet_id);
CREATE INDEX idx_bed_token ON balance_events_daily (token_id);
CREATE INDEX idx_bed_activity ON balance_events_daily (chain, network, watched_address, activity_type, block_time DESC);
CREATE INDEX idx_bed_wallet_activity ON balance_events_daily (wallet_id, activity_type, block_time DESC);

-- Step 5: Migrate existing data from old monthly partitions.
INSERT INTO balance_events_daily (
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
ON CONFLICT DO NOTHING;

-- Step 6: Swap tables - keep old table for rollback.
ALTER TABLE balance_events RENAME TO balance_events_monthly_old;
ALTER TABLE balance_events_daily RENAME TO balance_events;

-- Rename the default partition to match the new parent name.
ALTER TABLE balance_events_daily_default RENAME TO balance_events_default;

-- Step 7: Create partition manager function for auto-creating future daily partitions.
-- This function can be called from application code or pg_cron.
CREATE OR REPLACE FUNCTION create_daily_partitions(days_ahead INT DEFAULT 7)
RETURNS INT
LANGUAGE plpgsql
AS $fn$
DECLARE
    d INT;
    start_date DATE;
    end_date DATE;
    part_name TEXT;
    created INT := 0;
BEGIN
    FOR d IN 0..days_ahead LOOP
        start_date := CURRENT_DATE + d;
        end_date := start_date + INTERVAL '1 day';
        part_name := 'balance_events_' || to_char(start_date, 'YYYY_MM_DD');

        -- Use IF NOT EXISTS to make this idempotent.
        BEGIN
            EXECUTE format(
                'CREATE TABLE IF NOT EXISTS %I PARTITION OF balance_events
                 FOR VALUES FROM (%L) TO (%L)',
                part_name, start_date, end_date
            );
            -- Check if table was actually created (not already existing).
            IF FOUND THEN
                created := created + 1;
            END IF;
        EXCEPTION
            WHEN duplicate_table THEN
                -- Partition already exists, skip.
                NULL;
        END;
    END LOOP;

    RETURN created;
END;
$fn$;

-- Step 8: Create a function to reroute data stranded in the default partition
-- into proper daily partitions. Call after creating missing daily partitions.
CREATE OR REPLACE FUNCTION reroute_default_partition()
RETURNS INT
LANGUAGE plpgsql
AS $fn$
DECLARE
    moved INT := 0;
    batch_size INT := 5000;
    rows_moved INT;
BEGIN
    LOOP
        WITH to_move AS (
            DELETE FROM balance_events_default
            WHERE ctid IN (
                SELECT ctid FROM balance_events_default LIMIT batch_size
            )
            RETURNING *
        )
        INSERT INTO balance_events
        SELECT * FROM to_move
        ON CONFLICT DO NOTHING;
        GET DIAGNOSTICS rows_moved = ROW_COUNT;
        moved := moved + rows_moved;
        EXIT WHEN rows_moved = 0;
    END LOOP;
    RETURN moved;
END;
$fn$;

-- Step 9: Create a helper function to detach and drop old partitions for data retention.
CREATE OR REPLACE FUNCTION drop_old_daily_partitions(retention_days INT DEFAULT 90)
RETURNS INT
LANGUAGE plpgsql
AS $fn$
DECLARE
    cutoff_date DATE;
    rec RECORD;
    dropped INT := 0;
BEGIN
    cutoff_date := CURRENT_DATE - retention_days;

    FOR rec IN
        SELECT inhrelid::regclass::text AS part_name
        FROM pg_inherits
        WHERE inhparent = 'balance_events'::regclass
          AND inhrelid::regclass::text ~ '^balance_events_\d{4}_\d{2}_\d{2}$'
    LOOP
        -- Extract date from partition name (balance_events_YYYY_MM_DD).
        DECLARE
            part_date DATE;
        BEGIN
            part_date := to_date(
                substring(rec.part_name FROM 'balance_events_(\d{4}_\d{2}_\d{2})$'),
                'YYYY_MM_DD'
            );
            IF part_date < cutoff_date THEN
                EXECUTE format('ALTER TABLE balance_events DETACH PARTITION %I', rec.part_name);
                EXECUTE format('DROP TABLE IF EXISTS %I', rec.part_name);
                dropped := dropped + 1;
            END IF;
        END;
    END LOOP;

    RETURN dropped;
END;
$fn$;
