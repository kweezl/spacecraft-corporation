-- +goose Up
-- post_version records the format of a contract's forum-post starter message, so
-- a format change (the post can't be edited across formats — e.g. the Components
-- V2 flag is immutable) can be migrated by deleting the stale post and recreating
-- it. Version 1 = the original embed post; 2 = the Components V2 card. Every
-- existing row predates V2, so backfill 1; the app stamps the current version when
-- it (re)creates a post. No DEFAULT — the app supplies it (fails loudly otherwise),
-- matching the id/timestamp/kind convention.
ALTER TABLE contracts ADD COLUMN post_version INTEGER;
UPDATE contracts SET post_version = 1 WHERE post_version IS NULL;
ALTER TABLE contracts ALTER COLUMN post_version SET NOT NULL;

-- +goose Down
ALTER TABLE contracts DROP COLUMN post_version;
