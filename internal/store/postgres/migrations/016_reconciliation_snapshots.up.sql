CREATE TABLE IF NOT EXISTS balance_reconciliation_snapshots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain           TEXT NOT NULL,
    network         TEXT NOT NULL,
    address         TEXT NOT NULL,
    token_contract  TEXT NOT NULL DEFAULT '',
    on_chain_balance TEXT NOT NULL,
    db_balance      TEXT NOT NULL,
    difference      TEXT NOT NULL,
    is_match        BOOLEAN NOT NULL DEFAULT FALSE,
    checked_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_recon_snapshots_chain_network ON balance_reconciliation_snapshots(chain, network);
CREATE INDEX idx_recon_snapshots_checked_at ON balance_reconciliation_snapshots(checked_at);
CREATE INDEX idx_recon_snapshots_mismatch ON balance_reconciliation_snapshots(chain, network, is_match) WHERE NOT is_match;
