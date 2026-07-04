-- +goose Up
-- A server-owned contract template: reusable DEFAULT VALUES for contracts. A
-- contract instantiated from a template copies these values and stays fully
-- editable afterward — templates never affect existing contracts. id and
-- timestamps are app-supplied (no DB default); see create_servers.
CREATE TABLE contract_templates (
    id                           UUID          PRIMARY KEY,
    servers_id                   UUID          NOT NULL REFERENCES servers (id) ON DELETE RESTRICT,
    title                        TEXT          NOT NULL,
    description                  TEXT          NOT NULL DEFAULT '',
    -- Reward defaults copied onto contracts created from this template. Credits
    -- carry fractional amounts -> NUMERIC, never float. NOT NULL with 0 = "no
    -- reward" keeps the edit-page prefill pointer-free.
    reward_corpo_credits         NUMERIC(14,2) NOT NULL CHECK (reward_corpo_credits >= 0),
    reward_corpo_reputation      INTEGER       NOT NULL CHECK (reward_corpo_reputation >= 0),
    reward_corpo_licence_points  INTEGER       NOT NULL CHECK (reward_corpo_licence_points >= 0),
    -- Default deadline as a DURATION (templates are reusable; a fixed timestamp
    -- would go stale), stored as total minutes; 0 = no default deadline.
    -- Rendered/edited as days/hours/minutes like the contract create modal.
    deadline_minutes             INTEGER       NOT NULL CHECK (deadline_minutes >= 0),
    -- Delivery location: a gamedata space-object GDID plus the catalog version it
    -- was picked from (resolved via the gamedata Registry, falling back to the
    -- latest loaded version). NULL = not set.
    delivery_location_gdid       TEXT,
    delivery_location_gd_version TEXT,
    -- Simplified audit: who created the template / last touched it (Discord user IDs).
    created_by_user_id           TEXT          NOT NULL,
    updated_by_user_id           TEXT          NOT NULL,
    created_at                   TIMESTAMP     NOT NULL,
    updated_at                   TIMESTAMP     NOT NULL,
    -- Redundant with the PK, but it is the target the contracts provenance FK
    -- references as (id, servers_id) — making it structurally impossible for a
    -- contract to link a template owned by another server.
    UNIQUE (id, servers_id)
);

-- Case-insensitive title uniqueness per server: the title is the human search
-- and pick key, so duplicates would make the template picker ambiguous.
CREATE UNIQUE INDEX idx_contract_templates_title ON contract_templates (servers_id, lower(title));

-- +goose Down
DROP TABLE contract_templates;
