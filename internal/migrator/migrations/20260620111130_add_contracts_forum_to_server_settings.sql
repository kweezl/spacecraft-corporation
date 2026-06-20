-- +goose Up
-- The Discord forum channel a server designates for contracts: new contracts are
-- posted as threads here. NULL = unset (the contracts feature refuses /contract
-- create until an admin sets it via /contract forum).
ALTER TABLE server_settings ADD COLUMN contracts_forum_channel_id TEXT;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN contracts_forum_channel_id;
