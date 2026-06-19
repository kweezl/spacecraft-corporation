-- +goose Up
CREATE TABLE ping_log (
    id         BIGSERIAL PRIMARY KEY,
    server_id  TEXT        NOT NULL,
    user_id    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_ping_log_server_id ON ping_log (server_id);

-- +goose Down
DROP TABLE ping_log;
