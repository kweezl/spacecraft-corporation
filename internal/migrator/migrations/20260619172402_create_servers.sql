-- +goose Up
-- id is a UUIDv7 supplied by the application (time-ordered); the DB does not
-- generate it, so there is no DEFAULT.
CREATE TABLE servers (
    id         UUID        PRIMARY KEY,
    server_id  TEXT        NOT NULL UNIQUE,   -- Discord guild (server) snowflake
    name       TEXT        NOT NULL DEFAULT '',
    approved   BOOLEAN     NOT NULL DEFAULT false,
    -- Timezone-less; supplied by the application (configured timezone), no DB
    -- default — a forgotten insert fails loudly instead of storing a wrong zone.
    created_at TIMESTAMP   NOT NULL,
    updated_at TIMESTAMP   NOT NULL
);

-- +goose Down
DROP TABLE servers;
