-- +goose Up
-- A corporation-wide supply contract: members collaboratively deliver large
-- quantities of items before a deadline. Each contract is a Discord forum thread
-- (thread_id) whose starter message carries the live progress embed. id is an
-- app-supplied UUIDv7 and the timestamps are app-supplied (configured timezone),
-- so none has a DB default; see create_servers. Lifecycle is tracked by status
-- (no soft delete): 'cancelled'/'expired'/'completed' are terminal end states.
CREATE TABLE contracts (
    id                 UUID        PRIMARY KEY,
    -- The owning server: references servers.id (UUID PK). The app resolves the
    -- Discord snowflake to it. RESTRICT blocks deleting a server with contracts.
    servers_id         UUID        NOT NULL REFERENCES servers (id) ON DELETE RESTRICT,
    -- Discord forum thread snowflake; the starter message id equals it, so the
    -- progress embed is edited by targeting this id. Unique per bot. NULL until
    -- the outbox worker has created the thread (create is async — the contract row
    -- and a create-thread task are committed together, then the worker posts the
    -- forum thread and fills this in).
    thread_id          TEXT,
    title              TEXT        NOT NULL,
    description        TEXT        NOT NULL DEFAULT '',
    status             TEXT        NOT NULL,            -- open | completed | expired | cancelled
    -- Absolute instant the contract closes (now + entered duration), stored as a
    -- wall clock in the configured timezone like every other timestamp here.
    deadline           TIMESTAMP   NOT NULL,
    -- Discord user IDs for a simplified audit trail (see also _items/_reservations).
    created_by_user_id TEXT        NOT NULL,
    updated_by_user_id TEXT        NOT NULL,
    created_at         TIMESTAMP   NOT NULL,
    updated_at         TIMESTAMP   NOT NULL,
    closed_at          TIMESTAMP,                       -- set when status leaves 'open'
    CONSTRAINT contracts_status_check
        CHECK (status IN ('open', 'completed', 'expired', 'cancelled'))
);

-- In-thread commands resolve the contract by its thread; one contract per thread.
-- Partial so the (transient) NULLs of not-yet-created threads are excluded.
CREATE UNIQUE INDEX idx_contracts_thread ON contracts (thread_id) WHERE thread_id IS NOT NULL;
-- The expiry sweeper scans open contracts by deadline; partial so closed rows
-- stay out of the index.
CREATE INDEX idx_contracts_open_deadline ON contracts (deadline) WHERE status = 'open';
-- Listing a server's contracts.
CREATE INDEX idx_contracts_server ON contracts (servers_id);

-- +goose Down
DROP TABLE contracts;
