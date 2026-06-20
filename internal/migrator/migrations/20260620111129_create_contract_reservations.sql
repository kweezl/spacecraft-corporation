-- +goose Up
-- A member's commitment toward one contract item: reserved_qty is how much they
-- pledged to bring (participate), delivered_qty how much they have actually
-- handed in (deliver). One row per (item, member), upserted. id and timestamps
-- are app-supplied (no DB default); see create_servers.
CREATE TABLE contract_reservations (
    id                 UUID        PRIMARY KEY,
    contract_items_id  UUID        NOT NULL REFERENCES contract_items (id) ON DELETE RESTRICT,
    user_id            TEXT        NOT NULL,            -- the participating member (Discord user ID)
    reserved_qty       INTEGER     NOT NULL CHECK (reserved_qty >= 0),
    delivered_qty      INTEGER     NOT NULL DEFAULT 0 CHECK (delivered_qty >= 0),
    -- Simplified audit: who first reserved, and the last mutator (the member, or
    -- an officer using release-member). Discord user IDs.
    created_by_user_id TEXT        NOT NULL,
    updated_by_user_id TEXT        NOT NULL,
    created_at         TIMESTAMP   NOT NULL,
    updated_at         TIMESTAMP   NOT NULL,
    -- Can never have delivered more than reserved.
    CONSTRAINT reservation_delivery_bound CHECK (delivered_qty <= reserved_qty),
    -- One reservation row per member per item (participate upserts into it).
    UNIQUE (contract_items_id, user_id)
);

-- Aggregating reserved/delivered totals per item (progress + cap checks).
CREATE INDEX idx_reservations_item ON contract_reservations (contract_items_id);

-- +goose Down
DROP TABLE contract_reservations;
