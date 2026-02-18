DROP INDEX IF EXISTS idx_deny_log_chain;
DROP INDEX IF EXISTS idx_deny_log_token;
DROP TABLE IF EXISTS token_deny_log;

DROP INDEX IF EXISTS idx_tokens_denied;

ALTER TABLE tokens
    DROP COLUMN IF EXISTS denied_source,
    DROP COLUMN IF EXISTS scam_signals,
    DROP COLUMN IF EXISTS scam_score,
    DROP COLUMN IF EXISTS denied_at,
    DROP COLUMN IF EXISTS denied_reason;
