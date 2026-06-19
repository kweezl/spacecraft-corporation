-- +goose Up
-- Per-server role mapping for command access control: a command is granted to
-- zero or more Discord roles (any-of). id is a UUIDv7 supplied by the
-- application (time-ordered), so there is no DEFAULT. servers_id + command +
-- role_id is unique so a role can't be granted twice; the lookup index serves
-- the access gate's per-(server, command) read on every gated interaction.
-- Timestamps are supplied by the application (configured timezone), never the
-- DB — created_at has no DEFAULT so a forgotten insert fails loudly instead of
-- silently storing a wrong-zone time.
CREATE TABLE permissions (
    id                 UUID        PRIMARY KEY,
    -- References servers.id (UUID PK); the app resolves the Discord snowflake to
    -- it in SQL. RESTRICT blocks deleting a server that still has grants.
    servers_id         UUID        NOT NULL REFERENCES servers (id) ON DELETE RESTRICT,
    command            TEXT        NOT NULL,     -- slash command name
    role_id            TEXT        NOT NULL,     -- Discord role snowflake
    created_by_user_id TEXT        NOT NULL,     -- Discord user who granted it
    created_at         TIMESTAMP   NOT NULL,
    UNIQUE (servers_id, command, role_id)
);

CREATE INDEX idx_permissions_lookup ON permissions (servers_id, command);

-- +goose Down
DROP TABLE permissions;
