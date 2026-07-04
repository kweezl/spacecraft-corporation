-- +goose Up
-- A required line item on a contract template, keyed by gamedata id: the item's
-- GDID plus the catalog version it was picked from (unlike contract_items there
-- is no free-text path — templates postdate the gamedata integration). id and
-- timestamps are app-supplied (no DB default); see create_servers.
CREATE TABLE contract_template_items (
    id                    UUID      PRIMARY KEY,
    contract_templates_id UUID      NOT NULL REFERENCES contract_templates (id) ON DELETE RESTRICT,
    item_gdid             TEXT      NOT NULL,
    gamedata_version      TEXT      NOT NULL,
    quantity              INTEGER   NOT NULL CHECK (quantity > 0),
    -- Simplified audit: who added the item / last touched it (Discord user IDs).
    created_by_user_id    TEXT      NOT NULL,
    updated_by_user_id    TEXT      NOT NULL,
    created_at            TIMESTAMP NOT NULL,
    updated_at            TIMESTAMP NOT NULL
);

-- One line per item per template; also the lookup index. RESTRICT (house
-- convention): DeleteTemplate removes the items explicitly in the same tx.
CREATE UNIQUE INDEX idx_contract_template_items_gdid
    ON contract_template_items (contract_templates_id, item_gdid);

-- +goose Down
DROP TABLE contract_template_items;
