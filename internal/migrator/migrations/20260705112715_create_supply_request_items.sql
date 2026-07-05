-- +goose Up
-- One requested item on a supply request. Unlike contract items, supply items
-- are gamedata-native: item_gdid + gamedata_version are both NOT NULL (there is
-- no free-text path), so the panel and card resolve names from the versioned
-- catalog by gdid rather than a stored name snapshot. id and timestamps are
-- app-supplied (no DB default); see create_servers.
CREATE TABLE supply_request_items (
    id                 UUID      PRIMARY KEY,
    supply_requests_id UUID      NOT NULL REFERENCES supply_requests (id) ON DELETE RESTRICT,
    item_gdid          TEXT      NOT NULL,            -- gamedata item id
    gamedata_version   TEXT      NOT NULL,            -- catalog version it was picked from
    required_qty       INTEGER   NOT NULL CHECK (required_qty > 0),
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL,
    -- The same item may not appear twice on a request.
    UNIQUE (supply_requests_id, item_gdid)
);

-- Loading a request's items.
CREATE INDEX idx_supply_request_items_request ON supply_request_items (supply_requests_id);

-- +goose Down
DROP TABLE supply_request_items;
