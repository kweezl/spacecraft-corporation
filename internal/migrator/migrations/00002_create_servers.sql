-- +goose Up
-- gen_random_uuid() is built into PostgreSQL core (>=13), so no extension needed.
CREATE TABLE servers (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id  TEXT        NOT NULL UNIQUE,   -- Discord guild (server) snowflake
    name       TEXT        NOT NULL DEFAULT '',
    approved   BOOLEAN     NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Append-only audit of bot membership lifecycle (joined, removed, ...).
-- Kept independent of servers (no FK) so the log survives even if a server row
-- is ever pruned.
CREATE TABLE server_event (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id  TEXT        NOT NULL,          -- Discord guild (server) snowflake
    event_type TEXT        NOT NULL,          -- 'joined' | 'removed'
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_server_event_server_id ON server_event (server_id);

-- +goose Down
DROP TABLE server_event;
DROP TABLE servers;