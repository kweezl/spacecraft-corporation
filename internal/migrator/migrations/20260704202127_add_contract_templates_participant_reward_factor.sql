-- +goose Up
-- Default participant reward factor copied onto contracts created from this
-- template: percent (0–100) of the contract's corpo-credit reward distributed
-- to participants on completion. NOT NULL with 0 = "no participant rewards",
-- like the template reward columns. Backfill-then-lock (no DB DEFAULT — values
-- are app-supplied like every other column here; see add_contracts_kind).
ALTER TABLE contract_templates ADD COLUMN participant_reward_factor NUMERIC(5,2);
UPDATE contract_templates SET participant_reward_factor = 0 WHERE participant_reward_factor IS NULL;
ALTER TABLE contract_templates ALTER COLUMN participant_reward_factor SET NOT NULL;
ALTER TABLE contract_templates ADD CONSTRAINT contract_templates_factor_check
    CHECK (participant_reward_factor >= 0 AND participant_reward_factor <= 100);

-- +goose Down
ALTER TABLE contract_templates DROP COLUMN participant_reward_factor;
