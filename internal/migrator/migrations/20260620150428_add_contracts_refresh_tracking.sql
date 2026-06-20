-- +goose Up
-- Periodic keep-warm refresh + pre-expiry notice tracking for contracts.
--
-- last_refreshed_at: when the progress embed was last (re-)rendered. Advanced by
-- every embed-editing path (mutations, the keep-warm sweep, and close) so it is
-- the single source of truth for both "how stale is the post" (the sweeper's
-- keep-warm scan) and the "last updated" timestamp shown in the embed footer.
-- App-supplied like every timestamp here (no DB default); backfilled to
-- updated_at for existing rows before the NOT NULL constraint.
ALTER TABLE contracts ADD COLUMN last_refreshed_at TIMESTAMP;
UPDATE contracts SET last_refreshed_at = updated_at;
ALTER TABLE contracts ALTER COLUMN last_refreshed_at SET NOT NULL;

-- expiry_notified_at: when the one-shot "closing soon" participant ping was sent
-- (NULL = not yet). A latch, so the notice fires exactly once per contract.
ALTER TABLE contracts ADD COLUMN expiry_notified_at TIMESTAMP;

-- The keep-warm sweep scans open contracts by how long since they were last
-- rendered; partial so closed rows stay out of the index.
CREATE INDEX idx_contracts_refresh_due ON contracts (last_refreshed_at) WHERE status = 'open';
-- The pre-expiry notice scan: open, not-yet-notified contracts ordered by
-- deadline. Both predicate columns are immutable, so the partial index is valid.
CREATE INDEX idx_contracts_expiry_notify ON contracts (deadline)
    WHERE status = 'open' AND expiry_notified_at IS NULL;

-- +goose Down
DROP INDEX idx_contracts_expiry_notify;
DROP INDEX idx_contracts_refresh_due;
ALTER TABLE contracts DROP COLUMN expiry_notified_at;
ALTER TABLE contracts DROP COLUMN last_refreshed_at;
