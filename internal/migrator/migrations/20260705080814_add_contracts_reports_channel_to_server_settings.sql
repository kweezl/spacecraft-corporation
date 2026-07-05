-- +goose Up
-- The Discord text channel a server designates for contract payout reports:
-- completed contracts post their payout report + CSV here. NULL = unset (the
-- payout task skips posting with a warning until an admin sets it via /settings).
ALTER TABLE server_settings ADD COLUMN contracts_reports_channel_id TEXT;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN contracts_reports_channel_id;
