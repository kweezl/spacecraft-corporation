-- +goose Up
-- One participant's computed credit reward for a completed contract, written
-- once by the payout outbox task (compute + insert in one tx, ON CONFLICT DO
-- NOTHING) and then only read: the posted report, its CSV attachment, and the
-- console reprint all render from these rows, so retries and reprints never
-- recompute (catalog price drift can't change posted figures). id and
-- created_at are app-supplied (no DB default); see create_servers.
CREATE TABLE contract_payouts (
    id            UUID          PRIMARY KEY,
    contracts_id  UUID          NOT NULL REFERENCES contracts (id) ON DELETE RESTRICT,
    user_id       TEXT          NOT NULL,            -- the participant (Discord user ID)
    -- Display-name snapshot taken at compute time (nick > global name >
    -- username; falls back to the raw user ID when the member is gone) so the
    -- CSV and later reports show human names even after members leave.
    user_name     TEXT          NOT NULL,
    -- The participant's credit reward, truncated to 2dp (never rounded up, so
    -- the sum over a contract can't exceed its pool).
    amount        NUMERIC(14,2) NOT NULL CHECK (amount >= 0),
    -- The participant's value share of the pool, percent.
    share_percent NUMERIC(9,6)  NOT NULL CHECK (share_percent >= 0 AND share_percent <= 100),
    created_at    TIMESTAMP     NOT NULL,
    -- One payout row per participant per contract; the compute step's
    -- ON CONFLICT target.
    UNIQUE (contracts_id, user_id)
);

-- Rendering a contract's payout report reads all its rows.
CREATE INDEX idx_contract_payouts_contract ON contract_payouts (contracts_id);

-- +goose Down
DROP TABLE contract_payouts;
