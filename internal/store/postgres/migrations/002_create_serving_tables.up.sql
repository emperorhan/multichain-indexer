-- Serving data tables

CREATE TABLE tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain           VARCHAR(20) NOT NULL,
    network         VARCHAR(20) NOT NULL,
    contract_address VARCHAR(128) NOT NULL,
    symbol          VARCHAR(20) NOT NULL,
    name            VARCHAR(100) NOT NULL,
    decimals        INT NOT NULL,
    token_type      VARCHAR(20) NOT NULL DEFAULT 'FUNGIBLE',
    is_denied       BOOLEAN NOT NULL DEFAULT false,
    chain_data      JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_token UNIQUE (chain, network, contract_address)
);

CREATE TABLE transactions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain           VARCHAR(20) NOT NULL,
    network         VARCHAR(20) NOT NULL,
    tx_hash         VARCHAR(128) NOT NULL,
    block_cursor    BIGINT NOT NULL,
    block_time      TIMESTAMPTZ,
    fee_amount      NUMERIC(78,0) NOT NULL DEFAULT 0,
    fee_payer       VARCHAR(128) NOT NULL,
    status          VARCHAR(20) NOT NULL,
    err             TEXT,
    chain_data      JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_transaction UNIQUE (chain, network, tx_hash)
);

CREATE INDEX idx_tx_chain_cursor ON transactions (chain, network, block_cursor);
CREATE INDEX idx_tx_fee_payer ON transactions (fee_payer);
CREATE INDEX idx_tx_block_time ON transactions (block_time);

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

CREATE TABLE balances (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain                       VARCHAR(20) NOT NULL,
    network                     VARCHAR(20) NOT NULL,
    address                     VARCHAR(128) NOT NULL,
    token_id                    UUID NOT NULL REFERENCES tokens(id),
    wallet_id                   VARCHAR(100),
    organization_id             VARCHAR(100),
    amount                      NUMERIC(78,0) NOT NULL DEFAULT 0,
    pending_withdrawal_amount   NUMERIC(78,0) NOT NULL DEFAULT 0,
    last_updated_cursor         BIGINT NOT NULL DEFAULT 0,
    last_updated_tx_hash        VARCHAR(128),
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_balance UNIQUE (chain, network, address, token_id)
);

CREATE INDEX idx_balances_wallet ON balances (wallet_id);
CREATE INDEX idx_balances_address ON balances (chain, network, address);
