-- +goose Up
CREATE TABLE bot_tokens (
    id         BIGSERIAL PRIMARY KEY,
    guild_id   TEXT        NOT NULL,
    token      TEXT        NOT NULL,
    enabled    BOOLEAN     NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE bot_tokens;
