-- +goose Up
-- A member- or corp-owned base on a planet, surfaced so other members can find
-- and visit it to exchange resources. id is an app-supplied UUIDv7 and the
-- timestamps are app-supplied (configured timezone), so none has a DB default;
-- see create_servers. Soft-deleted via deleted_at (NULL = live) so unregister is
-- reversible and the base's history is retained.
CREATE TABLE bases (
    id                 UUID        PRIMARY KEY,
    -- The owning server: references servers.id (UUID PK). The app resolves the
    -- Discord snowflake to it in SQL. RESTRICT blocks deleting a server that
    -- still has base rows.
    servers_id         UUID        NOT NULL REFERENCES servers (id) ON DELETE RESTRICT,
    -- 'member' = owned by an individual member (owner_user_id set); 'corp' =
    -- owned by the server/corporation (owner_user_id NULL).
    kind               TEXT        NOT NULL,
    -- Discord user ID of the member who owns this base; NULL for corp bases.
    owner_user_id      TEXT,
    name               TEXT        NOT NULL,
    sector_name        TEXT        NOT NULL,
    system_code        TEXT        NOT NULL,
    -- Planet position in the system, 1..10 (rendered as Roman numerals I..X).
    planet_number      SMALLINT    NOT NULL,
    -- Discord user ID of whoever registered the row; differs from owner_user_id
    -- when a manager registers a base on behalf of a member.
    created_by_user_id TEXT        NOT NULL,
    created_at         TIMESTAMP   NOT NULL,
    updated_at         TIMESTAMP   NOT NULL,
    deleted_at         TIMESTAMP,                  -- NULL = live; set = soft-deleted
    CONSTRAINT bases_kind_check CHECK (kind IN ('member', 'corp')),
    -- A member base must name an owner; a corp base must not.
    CONSTRAINT bases_owner_check CHECK (
        (kind = 'member' AND owner_user_id IS NOT NULL) OR
        (kind = 'corp'   AND owner_user_id IS NULL)
    )
);

-- Listing/filtering the live bases on a server (the /base list query); partial
-- so soft-deleted rows stay out of the index.
CREATE INDEX idx_bases_server_live ON bases (servers_id) WHERE deleted_at IS NULL;
-- Counting and ownership-scoping a member's live bases (own-base limit + the
-- ownership predicate guarding equipment/unregister mutations).
CREATE INDEX idx_bases_owner_live ON bases (servers_id, owner_user_id) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE bases;
