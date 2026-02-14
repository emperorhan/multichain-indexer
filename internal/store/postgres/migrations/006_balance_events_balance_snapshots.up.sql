-- Add pre/post balance snapshots for each canonical balance event.
ALTER TABLE balance_events
    ADD COLUMN balance_before NUMERIC(78,0),
    ADD COLUMN balance_after NUMERIC(78,0);

CREATE INDEX idx_balance_events_balance_before
    ON balance_events (balance_before);
CREATE INDEX idx_balance_events_balance_after
    ON balance_events (balance_after);
