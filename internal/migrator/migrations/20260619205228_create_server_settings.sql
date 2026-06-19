-- +goose Up
-- Per-server localization settings: the theme (wording skin) and language the
-- bot renders messages in. Both are nullable — NULL means "use the app default"
-- (APP_THEME / APP_LANGUAGE), resolved at render time. id is an app-supplied
-- UUIDv7 and timestamps are app-supplied (configured timezone), so neither has a
-- DB default; see create_servers.
CREATE TABLE server_settings (
    id         UUID        PRIMARY KEY,
    -- One row per server: references servers.id (UUID PK), unique. The app
    -- resolves the Discord snowflake to it in SQL. RESTRICT blocks deleting a
    -- server that still has a settings row.
    servers_id UUID        NOT NULL UNIQUE REFERENCES servers (id) ON DELETE RESTRICT,
    theme      TEXT,                          -- NULL = app default
    language   TEXT,                          -- NULL = app default
    created_at TIMESTAMP   NOT NULL,
    updated_at TIMESTAMP   NOT NULL
);

-- +goose Down
DROP TABLE server_settings;
