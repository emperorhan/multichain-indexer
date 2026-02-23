-- Allow negative balances to support mid-history sync scenarios.
-- When syncing an existing wallet from a specific block, withdrawals may
-- precede deposits causing legitimate negative intermediate balances.
-- Developers handle these via DB (backfill or manual correction).
ALTER TABLE balances DROP CONSTRAINT IF EXISTS chk_balance_non_negative;
