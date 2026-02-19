CREATE TABLE IF NOT EXISTS indexed_blocks (
    chain           VARCHAR(20) NOT NULL,
    network         VARCHAR(20) NOT NULL,
    block_number    BIGINT NOT NULL,
    block_hash      VARCHAR(128) NOT NULL,
    parent_hash     VARCHAR(128) NOT NULL DEFAULT '',
    finality_state  VARCHAR(20) NOT NULL DEFAULT 'pending',
    block_time      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, network, block_number)
);

CREATE INDEX IF NOT EXISTS idx_indexed_blocks_unfinalized
    ON indexed_blocks (chain, network, block_number)
    WHERE finality_state NOT IN ('finalized');
