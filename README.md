# SpaceCraft Corporation

A companion Discord bot for the Steam game
[**SpaceCraft**](https://store.steampowered.com/app/3276050/SpaceCraft/).

## Purpose

SpaceCraft Corporation is a planning aid and in-game information assistant for
players and corporations. It runs as a single bot identity that you own and add
to any number of Discord servers via an OAuth2 invite link — there are no
per-server tokens to manage.

What it does today:

- **Member & corporation base registry** — register, list, and equip bases
  (extractors and production facilities) with per-tier and per-owner access
  control.
- **Contracts** — post and track corporation contracts in a forum channel, with
  live deadlines and automatic "closing soon" / expiry notices.
- **Role-based command access** — server owners and admins map Discord roles to
  commands; without it configured, commands are open.
- **Per-server localization** — each server can pick a wording theme and
  language; the bot ships English and Russian in `standard` and `lore` themes.
- **Server approval allowlist** — commands are only dispatched for servers the
  bot owner has approved, so you control where the single bot identity is usable.

Each feature can be enabled or disabled independently (see
[Configuration](#configuration)).

## Discord permissions

When you generate the bot's OAuth2 invite link (and in each server's role /
channel settings), grant it at least:

- **View Channel**, **Send Messages**, **Send Messages in Threads**, and
  **Create Posts** on the contracts forum channel — to post and update contracts
  and to leave the "closing soon" notice.
- **Manage Threads** on the contracts forum — to lock a contract's post when the
  contract closes, and to delete-and-repost a contract when its post format
  changes (a one-time migration the bot performs after an upgrade). Without it,
  the bot can still create and edit posts, but it cannot delete a post that
  members have already replied to: such a migration stays pending (the bot logs a
  hint) until you grant the permission or remove the post's comments.

## Running the bot (published image)

The release image is published to Docker Hub:

**https://hub.docker.com/r/kweezls/spacecraft-corporation**

The production Compose file pulls this image rather than building it — continuous
integration builds and pushes a multi-architecture image (`linux/amd64` and
`linux/arm64`) on every `vX.X.X` git tag.

1. Copy the example environment file and fill in your values:

   ```sh
   cp .env.example .env
   ```

   At minimum set `BOT_TOKEN` (your Discord bot token) and the `POSTGRES_*`
   credentials. See [Configuration](#configuration).

2. Start the stack:

   ```sh
   docker compose up
   ```

   This starts PostgreSQL, runs database migrations to completion as a one-shot
   step, then starts the bot.

Select which release to run with `IMAGE_TAG` (default `latest`):

```sh
IMAGE_TAG=1.2.3 docker compose up
```

## Building the image yourself

The whole project builds inside Docker — no host Go toolchain is required. The
`Dockerfile` is multi-stage with named targets (`build`, `prod`, `dev`).

Build the production image locally:

```sh
docker build --target prod --build-arg APP_VERSION=1.2.3 -t spacecraft-corporation:local .
```

`APP_VERSION` is baked into the binary and surfaced in logs and the version
field; it defaults to `dev` when unset.

## Local development

The dev stack runs the bot with hot reload (rebuilds on save) and an attachable
step debugger, with the source tree bind-mounted into the container.

```sh
make dev.up        # build and start the full dev stack (detached)
make dev.down      # stop and remove the stack, including the dev database
```

Other useful targets (run `make help` for the full list):

```sh
make dev.up-infra  # start just PostgreSQL + run migrations
make dev.up-app    # apply pending migrations, then run only the bot (foreground)
make dev.fmt       # format Go files
make dev.mock      # regenerate mocks
make dev.migration name=create_foo   # scaffold a timestamped migration
```

After adding a migration, re-run the migrate step so the hot-reloaded bot sees
the new schema (it no longer migrates on startup).

A remote debugger port is exposed for step debugging — attach your IDE to
`localhost:2345`.

## Configuration

All configuration is via environment variables, documented with defaults and
explanations in **[`.env.example`](.env.example)**. Copy it to `.env` and adjust.

The configuration follows a few consistent conventions:

- **Decentralized.** There is no single master config object. Each module reads
  only its own variables, so the variable name tells you which subsystem owns it
  (`POSTGRES_*` for the database, `BOT_*` for the Discord session, `BASES_*` /
  `CONTRACTS_*` for those features, and so on).

- **Sensible defaults.** Most variables have working defaults and can be left
  unset. The values you almost always must provide yourself are the secret/
  identity ones: `BOT_TOKEN` and the database credentials.

- **Secrets prefer files.** Sensitive values support a `_FILE` companion
  variable (`BOT_TOKEN_FILE`, `POSTGRES_PASSWORD_FILE`) that points at a mounted
  secret file. The file form **wins** when both are set, and avoids the leak
  paths that plain environment variables expose. Use it in production; the
  direct variable is fine for local development.

- **Single source of truth for Postgres.** The same `POSTGRES_*` values
  configure both the database container and the bot's connection — there is no
  separate connection-string variable to keep in sync.

- **Features are an allowlist.** `FEATURES` is a comma-separated list of enabled
  features. Unset means *all* known features; empty means *none*. Enabling a
  feature automatically enables anything it requires. A feature's own variables
  (e.g. the `BASES_*` limits) are only read when that feature is enabled.

- **Build/Compose variables.** A few variables (`IMAGE_TAG`, `APP_VERSION`,
  `UID`/`GID`) are consumed by Docker and Compose rather than the bot itself.
  They have working defaults and are documented in `.env.example`.

## License

[MIT](LICENSE) © kweezl