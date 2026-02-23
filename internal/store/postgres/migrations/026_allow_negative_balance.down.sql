-- Restore non-negative constraint. Rows with negative amounts must be
-- corrected before this migration can be applied.
ALTER TABLE balances
    ADD CONSTRAINT chk_balance_non_negative CHECK (amount >= 0) NOT VALID;

ALTER TABLE balances
    VALIDATE CONSTRAINT chk_balance_non_negative;
