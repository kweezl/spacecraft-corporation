-- +goose Up
-- A gamedata-linked contract item: the item's GDID + the catalog version it was
-- picked from. Legacy free-text rows keep both NULL and keep working. item_name
-- stays NOT NULL for every row — for gdid items it is the localized-name
-- snapshot taken at add time, because the public panel resolves items by
-- lower(item_name) (see lockItem in the contracts repository).
ALTER TABLE contract_items ADD COLUMN item_gdid TEXT;
ALTER TABLE contract_items ADD COLUMN gamedata_version TEXT;
ALTER TABLE contract_items ADD CONSTRAINT contract_items_gd_pair_check
    CHECK ((item_gdid IS NULL) = (gamedata_version IS NULL));

-- The same gdid may not appear twice on a contract even if differing localized
-- name snapshots would slip past the case-insensitive name index (which stays:
-- it is what panel name resolution relies on).
CREATE UNIQUE INDEX idx_contract_items_gdid
    ON contract_items (contracts_id, item_gdid) WHERE item_gdid IS NOT NULL;

-- +goose Down
DROP INDEX idx_contract_items_gdid;
ALTER TABLE contract_items DROP CONSTRAINT contract_items_gd_pair_check;
ALTER TABLE contract_items DROP COLUMN gamedata_version;
ALTER TABLE contract_items DROP COLUMN item_gdid;
