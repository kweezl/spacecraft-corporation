-- +goose Up
-- The fractional-digit precision (0–2) the participant payouts were computed at,
-- frozen on the contract row when the payout worker persists them (SavePayouts).
-- Rendering reads it back so a republish reproduces the original figures even if
-- CONTRACT_PAYOUT_DECIMALS is changed afterward — the same freeze-at-compute rule
-- as participant_reward_factor. Nullable: NULL = payouts not computed yet (fall
-- back to the current config for the first render, which then stamps this).
-- Bounded at 2 by the contract_payouts.amount NUMERIC(14,2) column.
ALTER TABLE contracts ADD COLUMN payout_decimals SMALLINT
    CHECK (payout_decimals IS NULL OR (payout_decimals >= 0 AND payout_decimals <= 2));

-- +goose Down
ALTER TABLE contracts DROP COLUMN payout_decimals;
