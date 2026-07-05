-- +goose Up
-- The location of a contract's already-posted payout report, so Reprint and
-- Mark-paid edit that one message in place instead of posting a duplicate. Both
-- NULL until the report is first posted; cleared implicitly by re-posting after
-- the message is deleted (a fresh id overwrites them).
ALTER TABLE contracts ADD COLUMN payout_report_channel_id TEXT;
ALTER TABLE contracts ADD COLUMN payout_report_message_id TEXT;

-- +goose Down
ALTER TABLE contracts DROP COLUMN payout_report_message_id;
ALTER TABLE contracts DROP COLUMN payout_report_channel_id;
