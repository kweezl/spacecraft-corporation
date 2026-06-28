-- +goose Up
-- A contract is either "custom" (authored field-by-field) or "template"
-- (instantiated from a server template). Kind governs which console actions and
-- permission key apply: custom contracts allow the full action set; template
-- contracts allow only deadline + cancel. Every existing contract predates
-- templates, so it is custom — backfill, then lock NOT NULL. No DEFAULT: the app
-- supplies kind on every insert (a forgotten value fails loudly), matching the
-- timestamp/id convention.
ALTER TABLE contracts ADD COLUMN kind TEXT;
UPDATE contracts SET kind = 'custom' WHERE kind IS NULL;
ALTER TABLE contracts ALTER COLUMN kind SET NOT NULL;
ALTER TABLE contracts ADD CONSTRAINT contracts_kind_check CHECK (kind IN ('custom', 'template'));

-- +goose Down
ALTER TABLE contracts DROP COLUMN kind;
