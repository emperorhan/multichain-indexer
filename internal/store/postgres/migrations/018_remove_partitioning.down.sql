-- Rollback: Restore daily-partitioned balance_events table from the flat table.

-- Guard: ensure old partitioned table still exists for rollback.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'balance_events_partitioned_old') THEN
        RAISE EXCEPTION 'Cannot rollback: balance_events_partitioned_old table does not exist.';
    END IF;
END $$;

-- Step 1: Rename flat table away.
ALTER TABLE balance_events RENAME TO balance_events_flat_bak;

-- Step 2: Restore the partitioned table.
ALTER TABLE balance_events_partitioned_old RENAME TO balance_events;

-- Step 3: Recreate the canonical_ids table.
CREATE TABLE IF NOT EXISTS balance_event_canonical_ids (
    chain                VARCHAR(20) NOT NULL,
    network              VARCHAR(20) NOT NULL,
    event_id             VARCHAR(512) NOT NULL,
    balance_event_id     UUID NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pk_balance_event_canonical_ids
        PRIMARY KEY (chain, network, event_id)
);
CREATE INDEX IF NOT EXISTS idx_balance_event_canonical_ids_event_lookup
    ON balance_event_canonical_ids (chain, network, event_id);
CREATE INDEX IF NOT EXISTS idx_balance_event_canonical_ids_balance_event
    ON balance_event_canonical_ids (balance_event_id);

-- Backfill canonical_ids from the partitioned table.
INSERT INTO balance_event_canonical_ids (chain, network, event_id, balance_event_id, created_at, updated_at)
SELECT chain, network, event_id, id, created_at, created_at
FROM (
    SELECT chain, network, event_id, id, created_at, block_time,
        ROW_NUMBER() OVER (PARTITION BY chain, network, event_id ORDER BY created_at DESC, block_time DESC, id) AS rn
    FROM balance_events
) b
WHERE b.rn = 1 AND b.event_id <> ''
ON CONFLICT (chain, network, event_id) DO NOTHING;

-- Step 4: Recreate partition manager functions.
CREATE OR REPLACE FUNCTION create_daily_partitions(days_ahead INT DEFAULT 7)
RETURNS INT LANGUAGE plpgsql AS $fn$
DECLARE d INT; start_date DATE; end_date DATE; part_name TEXT; created INT := 0;
BEGIN
    FOR d IN 0..days_ahead LOOP
        start_date := CURRENT_DATE + d;
        end_date := start_date + INTERVAL '1 day';
        part_name := 'balance_events_' || to_char(start_date, 'YYYY_MM_DD');
        BEGIN
            EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF balance_events FOR VALUES FROM (%L) TO (%L)', part_name, start_date, end_date);
            IF FOUND THEN created := created + 1; END IF;
        EXCEPTION WHEN duplicate_table THEN NULL;
        END;
    END LOOP;
    RETURN created;
END; $fn$;

CREATE OR REPLACE FUNCTION reroute_default_partition()
RETURNS INT LANGUAGE plpgsql AS $fn$
DECLARE moved INT := 0; batch_size INT := 5000; rows_moved INT;
BEGIN
    LOOP
        WITH to_move AS (
            DELETE FROM balance_events_default WHERE ctid IN (SELECT ctid FROM balance_events_default LIMIT batch_size) RETURNING *
        )
        INSERT INTO balance_events SELECT * FROM to_move ON CONFLICT DO NOTHING;
        GET DIAGNOSTICS rows_moved = ROW_COUNT;
        moved := moved + rows_moved;
        EXIT WHEN rows_moved = 0;
    END LOOP;
    RETURN moved;
END; $fn$;

CREATE OR REPLACE FUNCTION drop_old_daily_partitions(retention_days INT DEFAULT 90)
RETURNS INT LANGUAGE plpgsql AS $fn$
DECLARE cutoff_date DATE; rec RECORD; dropped INT := 0;
BEGIN
    cutoff_date := CURRENT_DATE - retention_days;
    FOR rec IN
        SELECT inhrelid::regclass::text AS part_name FROM pg_inherits
        WHERE inhparent = 'balance_events'::regclass AND inhrelid::regclass::text ~ '^balance_events_\d{4}_\d{2}_\d{2}$'
    LOOP
        DECLARE part_date DATE;
        BEGIN
            part_date := to_date(substring(rec.part_name FROM 'balance_events_(\d{4}_\d{2}_\d{2})$'), 'YYYY_MM_DD');
            IF part_date < cutoff_date THEN
                EXECUTE format('ALTER TABLE balance_events DETACH PARTITION %I', rec.part_name);
                EXECUTE format('DROP TABLE IF EXISTS %I', rec.part_name);
                dropped := dropped + 1;
            END IF;
        END;
    END LOOP;
    RETURN dropped;
END; $fn$;

-- Step 5: Drop the flat backup.
DROP TABLE IF EXISTS balance_events_flat_bak;
