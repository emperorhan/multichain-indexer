-- Recreate address_cursors table (rollback).
CREATE TABLE IF NOT EXISTS address_cursors (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain           VARCHAR(20) NOT NULL,
    network         VARCHAR(20) NOT NULL,
    address         VARCHAR(128) NOT NULL,
    cursor_value    VARCHAR(128),
    cursor_sequence BIGINT NOT NULL DEFAULT 0,
    items_processed BIGINT NOT NULL DEFAULT 0,
    last_fetched_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_address_cursor UNIQUE (chain, network, address)
);
