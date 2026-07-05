-- +goose Up
-- Deadlines are now optional: the game has contracts with no deadline. A
-- deadline-less contract never auto-expires and never gets a closing-soon
-- notice — the sweeper's scans require a deadline (deadline <= now / deadline
-- BETWEEN), so NULL rows are naturally skipped, and the partial indexes on
-- deadline keep working (a b-tree range scan never matches a NULL).
ALTER TABLE contracts ALTER COLUMN deadline DROP NOT NULL;

-- +goose Down
-- Reverting requires a value for any existing NULLs; refuse rather than guess.
-- Set a sentinel deadline on the NULL rows before re-applying NOT NULL if a
-- downgrade is ever needed.
ALTER TABLE contracts ALTER COLUMN deadline SET NOT NULL;
