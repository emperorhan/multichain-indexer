-- Create balance_events table to replace transfers

CREATE TABLE balance_events (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain                   VARCHAR(20) NOT NULL,
    network                 VARCHAR(20) NOT NULL,
    transaction_id          UUID NOT NULL REFERENCES transactions(id),
    tx_hash                 VARCHAR(128) NOT NULL,
    outer_instruction_index INT NOT NULL,
    inner_instruction_index INT NOT NULL DEFAULT -1,
    token_id                UUID NOT NULL REFERENCES tokens(id),
    activity_type           VARCHAR(40) NOT NULL,
    event_action            VARCHAR(60) NOT NULL,
    program_id              VARCHAR(128) NOT NULL,
    address                 VARCHAR(128) NOT NULL,
    counterparty_address    VARCHAR(128) NOT NULL DEFAULT '',
    delta                   NUMERIC(78,0) NOT NULL,
    watched_address         VARCHAR(128),
    wallet_id               VARCHAR(100),
    organization_id         VARCHAR(100),
    block_cursor            BIGINT NOT NULL,
    block_time              TIMESTAMPTZ,
    chain_data              JSONB NOT NULL DEFAULT '{}',
    balance_applied         BOOLEAN NOT NULL DEFAULT false,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_balance_event
        UNIQUE (chain, network, tx_hash, outer_instruction_index,
                inner_instruction_index, address, watched_address)
);

CREATE INDEX idx_balance_events_watched
    ON balance_events (chain, network, watched_address, block_time DESC);
CREATE INDEX idx_balance_events_cursor ON balance_events (chain, network, block_cursor);
CREATE INDEX idx_balance_events_address ON balance_events (address);
CREATE INDEX idx_balance_events_wallet ON balance_events (wallet_id);
CREATE INDEX idx_balance_events_token ON balance_events (token_id);
CREATE INDEX idx_balance_events_activity ON balance_events (activity_type);

-- Migrate existing data from transfers to balance_events
INSERT INTO balance_events (
    chain, network, transaction_id, tx_hash,
    outer_instruction_index, inner_instruction_index,
    token_id, activity_type, event_action, program_id,
    address, counterparty_address, delta,
    watched_address, wallet_id, organization_id,
    block_cursor, block_time, chain_data, created_at
)
SELECT
    t.chain, t.network, t.transaction_id, t.tx_hash,
    t.instruction_index, -1,
    t.token_id, 'DEPOSIT', 'legacy_transfer', '',
    CASE
        WHEN t.direction = 'DEPOSIT' THEN t.to_address
        WHEN t.direction = 'WITHDRAWAL' THEN t.from_address
        ELSE t.from_address
    END,
    CASE
        WHEN t.direction = 'DEPOSIT' THEN t.from_address
        WHEN t.direction = 'WITHDRAWAL' THEN t.to_address
        ELSE t.to_address
    END,
    CASE
        WHEN t.direction = 'WITHDRAWAL' THEN -t.amount
        ELSE t.amount
    END,
    t.watched_address, t.wallet_id, t.organization_id,
    t.block_cursor, t.block_time, t.chain_data, t.created_at
FROM transfers t
ON CONFLICT (chain, network, tx_hash, outer_instruction_index,
             inner_instruction_index, address, watched_address) DO NOTHING;

-- Drop old transfers table
DROP TABLE IF EXISTS transfers;
