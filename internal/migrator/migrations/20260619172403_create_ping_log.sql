-- +goose Up
CREATE TABLE ping_log (
    id         BIGSERIAL PRIMARY KEY,
    server_id  TEXT        NOT NULL,
    user_id    TEXT        NOT NULL,
    -- Timezone-less; default pinned to UTC wall-clock so the stored value does
    -- not depend on the session's TimeZone setting.
    created_at TIMESTAMP   NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);
CREATE INDEX idx_ping_log_server_id ON ping_log (server_id);

-- +goose Down
DROP TABLE ping_log;
