-- Add non-negative constraint on balances.amount.
-- Uses NOT VALID to avoid full table scan + lock, then VALIDATE separately.
ALTER TABLE balances
    ADD CONSTRAINT chk_balance_non_negative CHECK (amount >= 0) NOT VALID;

ALTER TABLE balances
    VALIDATE CONSTRAINT chk_balance_non_negative;
