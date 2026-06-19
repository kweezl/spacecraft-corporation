-- +goose Up
CREATE TABLE ping_log (
    id         BIGSERIAL PRIMARY KEY,
    -- References the servers row (UUID PK), not the raw Discord snowflake. The
    -- app resolves snowflake -> servers.id in SQL on insert. RESTRICT: a server
    -- can't be hard-deleted while it still has rows here.
    servers_id UUID        NOT NULL REFERENCES servers (id) ON DELETE RESTRICT,
    user_id    TEXT        NOT NULL,
    -- Timezone-less; supplied by the application (configured timezone), no DB
    -- default — a forgotten insert fails loudly instead of storing a wrong zone.
    created_at TIMESTAMP   NOT NULL
);
CREATE INDEX idx_ping_log_servers_id ON ping_log (servers_id);

-- +goose Down
DROP TABLE ping_log;
