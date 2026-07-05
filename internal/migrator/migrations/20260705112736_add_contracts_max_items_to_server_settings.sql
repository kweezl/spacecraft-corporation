-- +goose Up
-- Per-server cap on the distinct items a contract (or template) may require.
-- Replaces the CONTRACTS_MAX_ITEMS env var. NULL = unset, treated as the app
-- default (25) by the contracts feature. Owned by the contracts feature, stored
-- here like the forum channel.
ALTER TABLE server_settings ADD COLUMN contracts_max_items INTEGER
    CHECK (contracts_max_items IS NULL OR contracts_max_items > 0);

-- +goose Down
ALTER TABLE server_settings DROP COLUMN contracts_max_items;
