CREATE TABLE IF NOT EXISTS address_books (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain      TEXT NOT NULL,
    network    TEXT NOT NULL,
    org_id     TEXT NOT NULL DEFAULT '',
    address    TEXT NOT NULL,
    name       TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'ACTIVE',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_address_books_unique ON address_books(chain, network, org_id, address);
CREATE INDEX idx_address_books_chain_network ON address_books(chain, network);
CREATE INDEX idx_address_books_lookup ON address_books(chain, network, address) WHERE status = 'ACTIVE';
