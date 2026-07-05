-- +goose Up
-- A member's personal supply request: "I need these items". Lighter than a
-- contract — no deadline/sweeper, no rewards, no officer participation. Each
-- request is a Discord forum thread (thread_id) in the server's supply forum,
-- whose starter message carries the live progress card. id is an app-supplied
-- UUIDv7 and the timestamps are app-supplied (configured timezone), so none has
-- a DB default; see create_servers. Lifecycle is tracked by status (no soft
-- delete): 'completed'/'cancelled' are terminal end states. Strictly
-- self-scoped: every mutation is keyed on (servers_id, owner_user_id) in SQL.
CREATE TABLE supply_requests (
    id            UUID      PRIMARY KEY,
    -- The owning server: references servers.id (UUID PK). The app resolves the
    -- Discord snowflake to it. RESTRICT blocks deleting a server with requests.
    servers_id    UUID      NOT NULL REFERENCES servers (id) ON DELETE RESTRICT,
    -- The member who owns the request (Discord user ID). The ownership boundary:
    -- every owner-scoped mutation carries this in its WHERE, so a forged id
    -- affects zero rows.
    owner_user_id TEXT      NOT NULL,
    -- Discord forum thread snowflake; the starter message id equals it, so the
    -- card is edited by targeting this id. NULL until the outbox worker has
    -- created the thread (create is async: the request row and a create-thread
    -- task are committed together, then the worker posts and fills this in).
    thread_id     TEXT,
    title         TEXT      NOT NULL,
    description   TEXT      NOT NULL DEFAULT '',
    status        TEXT      NOT NULL,             -- open | completed | cancelled
    -- Card layout version, so a bot upgrade can re-render older posts.
    post_version  INTEGER   NOT NULL,

    -- Optional destination. A gamedata space-object delivery location (both
    -- columns set or both NULL, like a contract's), plus free-text system name /
    -- code and a planet position rendered as a Roman numeral on the card.
    delivery_location_gdid       TEXT,
    delivery_location_gd_version TEXT,
    system_name                  TEXT,
    system_code                  TEXT,
    planet_number                INTEGER,

    -- Optional Discord reference-message link, stored as its identifiers (never
    -- the URL): guild + channel + message snowflakes, reconstructed into a link
    -- on the card. The guild always equals this server's snowflake (validated at
    -- input). All three set together or all NULL.
    ref_message_guild_id   TEXT,
    ref_message_channel_id TEXT,
    ref_message_id         TEXT,

    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,
    closed_at     TIMESTAMP,                       -- set when status leaves 'open'

    CONSTRAINT supply_requests_status_check
        CHECK (status IN ('open', 'completed', 'cancelled')),
    CONSTRAINT supply_requests_location_pair_check
        CHECK ((delivery_location_gdid IS NULL) = (delivery_location_gd_version IS NULL)),
    CONSTRAINT supply_requests_planet_check
        CHECK (planet_number IS NULL OR planet_number > 0),
    CONSTRAINT supply_requests_ref_message_check
        CHECK ((ref_message_guild_id IS NULL) = (ref_message_channel_id IS NULL)
               AND (ref_message_channel_id IS NULL) = (ref_message_id IS NULL))
);

-- In-thread panel actions resolve the request by its thread; one per thread.
-- Partial so the (transient) NULLs of not-yet-created threads are excluded.
CREATE UNIQUE INDEX idx_supply_requests_thread ON supply_requests (thread_id) WHERE thread_id IS NOT NULL;
-- Listing / owner-scoped lookups.
CREATE INDEX idx_supply_requests_owner ON supply_requests (servers_id, owner_user_id);
-- The per-member open-request limit counts open rows; partial so closed rows
-- stay out of the index.
CREATE INDEX idx_supply_requests_open ON supply_requests (servers_id, owner_user_id) WHERE status = 'open';

-- +goose Down
DROP TABLE supply_requests;
