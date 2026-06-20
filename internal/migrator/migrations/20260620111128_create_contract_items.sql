-- +goose Up
-- A required line item on a contract: an item name and how much of it the
-- contract needs. Added per item via /contract item add (no inline parsing). id
-- and timestamps are app-supplied (no DB default); see create_servers.
CREATE TABLE contract_items (
    id                 UUID        PRIMARY KEY,
    contracts_id       UUID        NOT NULL REFERENCES contracts (id) ON DELETE RESTRICT,
    item_name          TEXT        NOT NULL,
    required_qty       INTEGER     NOT NULL CHECK (required_qty > 0),
    -- Simplified audit: who added the item / last touched it (Discord user IDs).
    created_by_user_id TEXT        NOT NULL,
    updated_by_user_id TEXT        NOT NULL,
    created_at         TIMESTAMP   NOT NULL,
    updated_at         TIMESTAMP   NOT NULL
);

-- Case-insensitive uniqueness so "Steel" and "steel" can't fragment a contract
-- into near-duplicate line items; also the lookup index for in-thread commands.
CREATE UNIQUE INDEX idx_contract_items_name ON contract_items (contracts_id, lower(item_name));

-- +goose Down
DROP TABLE contract_items;
