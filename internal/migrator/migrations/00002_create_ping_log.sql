-- +goose Up
CREATE TABLE ping_log (
    id         BIGSERIAL PRIMARY KEY,
    guild_id   TEXT        NOT NULL,
    user_id    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_ping_log_guild_id ON ping_log (guild_id);

-- +goose Down
DROP TABLE ping_log;
