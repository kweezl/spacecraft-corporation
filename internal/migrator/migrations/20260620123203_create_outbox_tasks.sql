-- +goose Up
-- Transactional outbox: a durable queue of side effects (e.g. Discord REST calls)
-- enqueued in the SAME transaction as the domain write that triggers them, so the
-- effect can never be lost on a crash and never sits on an interaction's deadline.
-- A background worker scans due rows, runs the registered handler for the kind,
-- and records success/retry/give-up. id and timestamps are app-supplied (no DB
-- default); see create_servers.
CREATE TABLE outbox_tasks (
    id          UUID        PRIMARY KEY,
    kind        TEXT        NOT NULL,            -- handler key (e.g. 'contracts.thread.create')
    -- payload schema version: handlers may evolve, and a task enqueued before a
    -- deploy can be run after it, so the handler can branch on this.
    version     SMALLINT    NOT NULL,
    payload     JSONB       NOT NULL,            -- handler-specific JSON
    -- Collapsing key (e.g. the contract id). Tasks are never coalesced at enqueue
    -- (each change is a durable row); instead the worker, per tick, runs only the
    -- newest task per (kind, chronometric_id) and supersedes the older ones. This
    -- avoids the lost-update race without locking: a concurrent change is a newer
    -- row that simply wins. NULL = no collapsing (the row groups by its own id).
    chronometric_id UUID,
    status      TEXT        NOT NULL,            -- pending | done | failed
    attempts    INTEGER     NOT NULL,            -- failed runs so far
    last_error  TEXT        NOT NULL,            -- '' until a run fails; kept for diagnosis
    next_try_at TIMESTAMP   NOT NULL,            -- when the task is next eligible (retry backoff)
    -- Set when the task is abandoned and must be ignored: retries exhausted, or a
    -- permanent/irrelevant condition (e.g. the bot was removed from the guild).
    -- NULL while pending and once done. Anchors failed-task retention.
    evacuated_at TIMESTAMP,
    created_at  TIMESTAMP   NOT NULL,
    updated_at  TIMESTAMP   NOT NULL,
    CONSTRAINT outbox_status_check CHECK (status IN ('pending', 'done', 'failed'))
);

-- The worker's scan: due pending tasks, oldest first. Partial so done/failed rows
-- stay out of the hot index.
CREATE INDEX idx_outbox_due ON outbox_tasks (next_try_at) WHERE status = 'pending';

-- +goose Down
DROP TABLE outbox_tasks;
