-- Coordinator queries indexed_blocks frequently by chain/network/height
CREATE INDEX IF NOT EXISTS idx_indexed_blocks_cursor
    ON indexed_blocks (chain, network, block_height DESC);

-- ConfigWatcher polls runtime_configs every 30s per chain/network
CREATE INDEX IF NOT EXISTS idx_runtime_configs_lookup
    ON runtime_configs (chain, network);
