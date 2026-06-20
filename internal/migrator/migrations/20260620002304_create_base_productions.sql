-- +goose Up
-- Production facilities installed on a base: each produces one named item. A base
-- may hold several, capped in the app via BASES_PRODUCTION_LIMIT. id is an
-- app-supplied UUIDv7 and created_at is app-supplied (configured timezone), so
-- neither has a DB default. Rows are hard-deleted on removal (not audited, unlike
-- the soft-deleted bases). Kept separate from base_extractors: a base's
-- extractors (raw resources) and productions (crafted items) are independent.
CREATE TABLE base_productions (
    id         UUID        PRIMARY KEY,
    -- The base this production belongs to. RESTRICT keeps the FK consistent;
    -- bases are soft-deleted, so the parent row is never hard-removed underneath.
    bases_id   UUID        NOT NULL REFERENCES bases (id) ON DELETE RESTRICT,
    item_name  TEXT        NOT NULL,
    created_at TIMESTAMP   NOT NULL
);

-- Listing and counting a base's productions.
CREATE INDEX idx_base_productions_base ON base_productions (bases_id);

-- +goose Down
DROP TABLE base_productions;
