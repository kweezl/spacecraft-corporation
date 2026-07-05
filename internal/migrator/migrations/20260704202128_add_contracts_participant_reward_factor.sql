-- +goose Up
-- Participant reward factor on the contract itself: percent (0–100) of
-- reward_corpo_credits distributed to participants when the contract completes.
-- Copied from the template (or the server default for custom contracts) at
-- creation, editable while open, never re-resolved. NOT NULL with 0 = "no
-- participant rewards"; backfill-then-lock like add_contracts_kind.
ALTER TABLE contracts ADD COLUMN participant_reward_factor NUMERIC(5,2);
UPDATE contracts SET participant_reward_factor = 0 WHERE participant_reward_factor IS NULL;
ALTER TABLE contracts ALTER COLUMN participant_reward_factor SET NOT NULL;
ALTER TABLE contracts ADD CONSTRAINT contracts_factor_check
    CHECK (participant_reward_factor >= 0 AND participant_reward_factor <= 100);

-- Latched by the payout outbox task once the payout report was successfully
-- posted to the contract thread — the idempotency marker for the task's
-- non-transactional Discord side effect (same at-least-once window as thread
-- creation). NULL = not posted (or nothing to post).
ALTER TABLE contracts ADD COLUMN payout_posted_at TIMESTAMP;

-- Set together when an officer presses "mark paid" on a completed contract:
-- when the participant payouts were handed out in game, and by whom (Discord
-- user ID). The pair doubles as the "payouts paid" marker; the check keeps the
-- two columns in lockstep.
ALTER TABLE contracts ADD COLUMN payouts_paid_at TIMESTAMP;
ALTER TABLE contracts ADD COLUMN payouts_paid_by_user_id TEXT;
ALTER TABLE contracts ADD CONSTRAINT contracts_payouts_paid_pair_check
    CHECK ((payouts_paid_at IS NULL) = (payouts_paid_by_user_id IS NULL));

-- +goose Down
ALTER TABLE contracts DROP CONSTRAINT contracts_payouts_paid_pair_check;
ALTER TABLE contracts DROP COLUMN payouts_paid_by_user_id;
ALTER TABLE contracts DROP COLUMN payouts_paid_at;
ALTER TABLE contracts DROP COLUMN payout_posted_at;
ALTER TABLE contracts DROP COLUMN participant_reward_factor;
