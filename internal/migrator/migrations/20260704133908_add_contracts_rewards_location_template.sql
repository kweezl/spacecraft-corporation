-- +goose Up
-- Rewards, delivery location, and template provenance on contracts. All columns
-- are nullable: every existing row predates the feature and custom contracts may
-- simply not set them. Values are COPIED from the template at instantiation —
-- later template edits/deletes never change a posted contract.
ALTER TABLE contracts ADD COLUMN reward_corpo_credits NUMERIC(14,2)
    CHECK (reward_corpo_credits IS NULL OR reward_corpo_credits >= 0);
ALTER TABLE contracts ADD COLUMN reward_corpo_reputation INTEGER
    CHECK (reward_corpo_reputation IS NULL OR reward_corpo_reputation >= 0);
ALTER TABLE contracts ADD COLUMN reward_corpo_licence_points INTEGER
    CHECK (reward_corpo_licence_points IS NULL OR reward_corpo_licence_points >= 0);
-- Delivery location: gamedata space-object GDID + the catalog version it was
-- picked from; both set or both NULL.
ALTER TABLE contracts ADD COLUMN delivery_location_gdid TEXT;
ALTER TABLE contracts ADD COLUMN delivery_location_gd_version TEXT;
ALTER TABLE contracts ADD CONSTRAINT contracts_location_pair_check
    CHECK ((delivery_location_gdid IS NULL) = (delivery_location_gd_version IS NULL));
-- Stats-only provenance link. Two deliberate choices:
--
-- ON DELETE SET NULL is an exception to the RESTRICT convention: RESTRICT is
-- for ownership edges where the child must not outlive the parent
-- (servers->contracts, contracts->items). This link is informational; RESTRICT
-- would make a template undeletable once used, while the requirement is that
-- template deletion must never break contracts. SET NULL degrades gracefully:
-- the contract keeps its copied values, only the provenance pointer is lost.
-- The column list (PG 15+) nulls just the pointer, never servers_id.
--
-- The FK is COMPOSITE, pairing the template id with the contract's own
-- servers_id against the (id, servers_id) unique on contract_templates: a
-- template belongs to exactly one server, and a contract can only ever link a
-- template of its own server — enforced structurally, not just in the app
-- layer. (MATCH SIMPLE: a NULL template id passes regardless, so custom
-- contracts are unaffected.)
ALTER TABLE contracts ADD COLUMN contract_templates_id UUID;
ALTER TABLE contracts ADD CONSTRAINT contracts_template_fk
    FOREIGN KEY (contract_templates_id, servers_id)
    REFERENCES contract_templates (id, servers_id)
    ON DELETE SET NULL (contract_templates_id);

-- For per-template usage stats and to speed the SET NULL sweep on delete.
CREATE INDEX idx_contracts_template ON contracts (contract_templates_id)
    WHERE contract_templates_id IS NOT NULL;

-- +goose Down
DROP INDEX idx_contracts_template;
ALTER TABLE contracts DROP COLUMN contract_templates_id;
ALTER TABLE contracts DROP COLUMN delivery_location_gd_version;
ALTER TABLE contracts DROP COLUMN delivery_location_gdid;
ALTER TABLE contracts DROP COLUMN reward_corpo_licence_points;
ALTER TABLE contracts DROP COLUMN reward_corpo_reputation;
ALTER TABLE contracts DROP COLUMN reward_corpo_credits;
