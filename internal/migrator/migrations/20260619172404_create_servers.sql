-- +goose Up
-- id is a UUIDv7 supplied by the application (time-ordered); the DB does not
-- generate it, so there is no DEFAULT.
CREATE TABLE servers (
    id         UUID        PRIMARY KEY,
    server_id  TEXT        NOT NULL UNIQUE,   -- Discord guild (server) snowflake
    name       TEXT        NOT NULL DEFAULT '',
    approved   BOOLEAN     NOT NULL DEFAULT false,
    -- Timezone-less; defaults pinned to UTC wall-clock (session-TZ independent).
    created_at TIMESTAMP   NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'),
    updated_at TIMESTAMP   NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);

-- +goose Down
DROP TABLE servers;
