-- +goose Up
-- Extractors installed on a base: each pulls one named resource. A base may hold
-- several, capped in the app via BASES_EXTRACTOR_LIMIT. id is an app-supplied
-- UUIDv7 and created_at is app-supplied (configured timezone), so neither has a
-- DB default. Rows are hard-deleted on removal (equipment is not audited, unlike
-- the soft-deleted bases).
CREATE TABLE base_extractors (
    id            UUID        PRIMARY KEY,
    -- The base this extractor belongs to. RESTRICT keeps the FK consistent;
    -- bases are soft-deleted, so the parent row is never hard-removed underneath.
    bases_id      UUID        NOT NULL REFERENCES bases (id) ON DELETE RESTRICT,
    resource_name TEXT        NOT NULL,
    created_at    TIMESTAMP   NOT NULL
);

-- Listing and counting a base's extractors.
CREATE INDEX idx_base_extractors_base ON base_extractors (bases_id);

-- +goose Down
DROP TABLE base_extractors;
