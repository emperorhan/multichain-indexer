-- Enforce deterministic canonical uniqueness for balance events.
ALTER TABLE balance_events
    ADD CONSTRAINT uq_balance_events_event_id UNIQUE (event_id);
