-- +goose Up
-- Append-only audit of bot membership lifecycle (joined, removed, ...).
-- Kept independent of servers (no FK) so the log survives even if a server row
-- is ever pruned.
CREATE TABLE server_event (
    -- id is an application-supplied UUIDv7 (time-ordered); no DB-side default.
    id         UUID        PRIMARY KEY,
    server_id  TEXT        NOT NULL,          -- Discord guild (server) snowflake
    event_type TEXT        NOT NULL,          -- 'joined' | 'removed'
    created_at TIMESTAMP   NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);
CREATE INDEX idx_server_event_server_id ON server_event (server_id);

-- +goose Down
DROP TABLE server_event;
