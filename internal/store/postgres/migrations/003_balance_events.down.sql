-- Recreate transfers table
CREATE TABLE transfers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain               VARCHAR(20) NOT NULL,
    network             VARCHAR(20) NOT NULL,
    transaction_id      UUID NOT NULL REFERENCES transactions(id),
    tx_hash             VARCHAR(128) NOT NULL,
    instruction_index   INT NOT NULL,
    token_id            UUID NOT NULL REFERENCES tokens(id),
    from_address        VARCHAR(128) NOT NULL,
    to_address          VARCHAR(128) NOT NULL,
    amount              NUMERIC(78,0) NOT NULL,
    direction           VARCHAR(10),
    watched_address     VARCHAR(128),
    wallet_id           VARCHAR(100),
    organization_id     VARCHAR(100),
    block_cursor        BIGINT NOT NULL,
    block_time          TIMESTAMPTZ,
    chain_data          JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_transfer UNIQUE (chain, network, tx_hash, instruction_index, watched_address)
);

CREATE INDEX idx_transfers_watched
    ON transfers (chain, network, watched_address, block_time DESC);
CREATE INDEX idx_transfers_cursor ON transfers (chain, network, block_cursor);
CREATE INDEX idx_transfers_from ON transfers (from_address);
CREATE INDEX idx_transfers_to ON transfers (to_address);
CREATE INDEX idx_transfers_wallet ON transfers (wallet_id);
CREATE INDEX idx_transfers_token ON transfers (token_id);

DROP TABLE IF EXISTS balance_events;
