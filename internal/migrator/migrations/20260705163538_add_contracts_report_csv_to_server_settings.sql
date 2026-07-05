-- +goose Up
-- Per-server toggle for attaching the payout CSV export to a completed
-- contract's report. Defaults to FALSE (no CSV) — the report is posted without
-- the attachment unless a server opts in via /settings. NOT NULL DEFAULT FALSE
-- so existing rows read as off and the contracts feature never needs COALESCE.
-- Owned by the contracts feature, stored here like the forum channel.
ALTER TABLE server_settings ADD COLUMN contracts_report_csv BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN contracts_report_csv;
