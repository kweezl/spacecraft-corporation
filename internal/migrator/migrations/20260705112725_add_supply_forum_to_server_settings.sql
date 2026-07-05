-- +goose Up
-- The Discord forum channel a server designates for supply requests: new
-- requests are posted as threads here. NULL = unset (the supply feature refuses
-- to create a request until an admin sets it via /settings). Owned by the supply
-- feature, stored here per the "extend settings" approach (like the contracts
-- forum channel).
ALTER TABLE server_settings ADD COLUMN supply_forum_channel_id TEXT;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN supply_forum_channel_id;
