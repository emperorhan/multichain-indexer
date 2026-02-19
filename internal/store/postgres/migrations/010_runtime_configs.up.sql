-- Runtime-overridable configuration table.
-- Allows hot-reload of pipeline parameters without restart.
-- Values in this table override environment-variable defaults at runtime.

CREATE TABLE runtime_configs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain           VARCHAR(20) NOT NULL,
    network         VARCHAR(20) NOT NULL,
    config_key      VARCHAR(100) NOT NULL,
    config_value    TEXT NOT NULL,
    description     TEXT,
    is_active       BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_runtime_config UNIQUE (chain, network, config_key)
);

CREATE INDEX idx_runtime_config_active
    ON runtime_configs (chain, network) WHERE is_active = true;

-- Also add rpc_url column to indexer_configs for hot-reloadable RPC endpoint.
ALTER TABLE indexer_configs ADD COLUMN IF NOT EXISTS rpc_url TEXT NOT NULL DEFAULT '';
