-- Add a stable canonical event-id routing table to keep idempotent upserts
-- deterministic under partition key drift.

CREATE TABLE balance_event_canonical_ids (
    chain                VARCHAR(20) NOT NULL,
    network              VARCHAR(20) NOT NULL,
    event_id             VARCHAR(512) NOT NULL,
    balance_event_id     UUID NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT pk_balance_event_canonical_ids
        PRIMARY KEY (chain, network, event_id)
);

CREATE INDEX idx_balance_event_canonical_ids_event_lookup
    ON balance_event_canonical_ids (chain, network, event_id);
CREATE INDEX idx_balance_event_canonical_ids_balance_event
    ON balance_event_canonical_ids (balance_event_id);

INSERT INTO balance_event_canonical_ids (
    chain,
    network,
    event_id,
    balance_event_id,
    created_at,
    updated_at
)
SELECT
    b.chain,
    b.network,
    b.event_id,
    b.id,
    b.created_at,
    b.created_at
FROM (
    SELECT
        chain,
        network,
        event_id,
        id,
        created_at,
        block_time,
        ROW_NUMBER() OVER (
            PARTITION BY chain, network, event_id
            ORDER BY created_at DESC, block_time DESC, id
        ) AS rn
    FROM balance_events
) b
WHERE b.rn = 1
  AND b.event_id <> ''
ON CONFLICT (chain, network, event_id)
DO UPDATE SET
    balance_event_id = EXCLUDED.balance_event_id,
    updated_at = EXCLUDED.updated_at;
