-- +goose Up
-- A member's commitment toward one supply-request item: reserved_qty is how much
-- they pledged to bring, delivered_qty how much they have actually handed in.
-- One row per (item, member), upserted. Mirrors contract_reservations. id and
-- timestamps are app-supplied (no DB default); see create_servers.
CREATE TABLE supply_reservations (
    id                      UUID      PRIMARY KEY,
    supply_request_items_id UUID      NOT NULL REFERENCES supply_request_items (id) ON DELETE RESTRICT,
    user_id                 TEXT      NOT NULL,            -- the reserving member (Discord user ID)
    reserved_qty            INTEGER   NOT NULL CHECK (reserved_qty >= 0),
    delivered_qty           INTEGER   NOT NULL DEFAULT 0 CHECK (delivered_qty >= 0),
    created_by_user_id      TEXT      NOT NULL,
    updated_by_user_id      TEXT      NOT NULL,
    created_at              TIMESTAMP NOT NULL,
    updated_at              TIMESTAMP NOT NULL,
    -- Can never have delivered more than reserved.
    CONSTRAINT supply_reservation_delivery_bound CHECK (delivered_qty <= reserved_qty),
    -- One reservation row per member per item (reserve upserts into it).
    UNIQUE (supply_request_items_id, user_id)
);

-- Aggregating reserved/delivered totals per item (progress + cap checks).
CREATE INDEX idx_supply_reservations_item ON supply_reservations (supply_request_items_id);

-- +goose Down
DROP TABLE supply_reservations;
