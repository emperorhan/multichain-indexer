-- Token scam detection: add deny metadata columns and audit log table

ALTER TABLE tokens
    ADD COLUMN IF NOT EXISTS denied_reason  VARCHAR(256) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS denied_at      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS scam_score     SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS scam_signals   JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS denied_source  VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_tokens_denied
    ON tokens (chain, network, is_denied) WHERE is_denied = true;

CREATE TABLE IF NOT EXISTS token_deny_log (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_id         UUID NOT NULL REFERENCES tokens(id),
    chain            VARCHAR(20) NOT NULL,
    network          VARCHAR(20) NOT NULL,
    contract_address VARCHAR(128) NOT NULL,
    action           VARCHAR(20) NOT NULL,   -- 'deny' / 'allow'
    reason           VARCHAR(256) NOT NULL DEFAULT '',
    source           VARCHAR(64) NOT NULL DEFAULT '',
    metadata         JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_deny_log_token ON token_deny_log (token_id);
CREATE INDEX idx_deny_log_chain ON token_deny_log (chain, network, created_at DESC);
