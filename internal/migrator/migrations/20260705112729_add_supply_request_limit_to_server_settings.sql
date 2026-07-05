-- +goose Up
-- Per-server cap on how many OPEN supply requests one member may have at once.
-- NULL = unset, treated as the app default (10) by the supply feature. Owned by
-- the supply feature, stored here like the forum channel.
ALTER TABLE server_settings ADD COLUMN supply_request_limit INTEGER
    CHECK (supply_request_limit IS NULL OR supply_request_limit > 0);

-- +goose Down
ALTER TABLE server_settings DROP COLUMN supply_request_limit;
