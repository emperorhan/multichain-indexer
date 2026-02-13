-- Pipeline state tables

CREATE TABLE indexer_configs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain               VARCHAR(20) NOT NULL,
    network             VARCHAR(20) NOT NULL,
    is_active           BOOLEAN NOT NULL DEFAULT false,
    target_batch_size   INT NOT NULL DEFAULT 100,
    indexing_interval_ms INT NOT NULL DEFAULT 5000,
    chain_config        JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_indexer_config UNIQUE (chain, network)
);

CREATE TABLE watched_addresses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain           VARCHAR(20) NOT NULL,
    network         VARCHAR(20) NOT NULL,
    address         VARCHAR(128) NOT NULL,
    wallet_id       VARCHAR(100),
    organization_id VARCHAR(100),
    label           VARCHAR(255),
    is_active       BOOLEAN NOT NULL DEFAULT true,
    source          VARCHAR(20) NOT NULL DEFAULT 'db',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_watched_address UNIQUE (chain, network, address)
);

CREATE INDEX idx_watched_addr_active
    ON watched_addresses (chain, network) WHERE is_active = true;

CREATE TABLE address_cursors (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain               VARCHAR(20) NOT NULL,
    network             VARCHAR(20) NOT NULL,
    address             VARCHAR(128) NOT NULL,
    cursor_value        VARCHAR(128),
    cursor_sequence     BIGINT NOT NULL DEFAULT 0,
    items_processed     BIGINT NOT NULL DEFAULT 0,
    last_fetched_at     TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_address_cursor UNIQUE (chain, network, address),
    CONSTRAINT fk_cursor_watched_addr
        FOREIGN KEY (chain, network, address)
        REFERENCES watched_addresses(chain, network, address)
);

CREATE TABLE pipeline_watermarks (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain               VARCHAR(20) NOT NULL,
    network             VARCHAR(20) NOT NULL,
    head_sequence       BIGINT NOT NULL DEFAULT 0,
    ingested_sequence   BIGINT NOT NULL DEFAULT 0,
    last_heartbeat_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_watermark UNIQUE (chain, network)
);
