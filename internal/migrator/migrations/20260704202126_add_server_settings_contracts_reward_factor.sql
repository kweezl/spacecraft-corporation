-- +goose Up
-- Per-server default participant reward factor: the percent of a contract's
-- corpo-credit reward distributed to participants on completion. Prefills new
-- contract templates and custom contracts (copy-at-instantiation — changing it
-- never affects existing rows). NULL = unset, treated as 0 ("participants get
-- nothing"), matching the nullable-column convention of server_settings.
ALTER TABLE server_settings ADD COLUMN contracts_participant_reward_factor NUMERIC(5,2)
    CHECK (contracts_participant_reward_factor IS NULL
           OR (contracts_participant_reward_factor >= 0 AND contracts_participant_reward_factor <= 100));

-- +goose Down
ALTER TABLE server_settings DROP COLUMN contracts_participant_reward_factor;
