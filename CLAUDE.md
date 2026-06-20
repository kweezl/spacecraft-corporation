# SpaceCraft Discord Bot

Companion Discord bot for the Steam game **SpaceCraft**
(https://store.steampowered.com/app/3276050/SpaceCraft/). Purpose: planning aid
and in-game information support for players. Long-term data model is "game
reference data + per-user planning state."

## Stack

- **Language:** Go
- **Discord library:** `bwmarrin/discordgo`
- **DI / modules:** `uber-go/fx` — every feature is an `fx.Module`, enable/disable independently
- **Database:** PostgreSQL via `pgx` / `pgxpool`
- **Migrations:** `goose`, SQL embedded via `embed.FS`, run as a one-shot
  `--migrate` step (not on bot startup)
- **Config:** `caarlos0/env`, decentralized per module (see Config)
- **Logging:** `uber-go/zap` — JSON encoder, stderr, stacktraces at error level and above
- **Testing:** TDD. Mocks via `mockery`. Assertions via `testify/assert` + `testify/require`
- **Hosting:** Docker Compose (bot + postgres). Code on GitHub.

## Bot model (single bot)

One bot identity that you own. It runs **a single `discordgo` session** from
`BOT_TOKEN` (env) and is added to other Discord **servers** via an OAuth2 invite
link — no per-server tokens, no token storage. To gate who can add it, turn off
"Public Bot" in the Developer Portal (only the app owner can invite) and/or rely
on the **server approval allowlist** (the `servers` module): commands from
unapproved servers are not dispatched — instead the bot replies that the server
must be approved by the bot owner (mentioning `APP_OWNER_DISCORD_ID` when set).
Slash commands are registered **per joined server** on `GuildCreate` (instant, vs
~1h for global), so there is no dev-server env var.

Terminology: **"server"** is our domain term for a Discord guild. discordgo's API
still says `guild` (e.g. `i.GuildID`); we read those into `server`-named values.

## fx modules

- **`appconfig`** — provides shared `AppConfig{ Name, Version, OwnerDiscordID }`.
  Knows nothing about other modules' settings. `Name` comes from the `APP_NAME`
  env var (default `spacecraft-corporation`); `OwnerDiscordID` from the optional
  `APP_OWNER_DISCORD_ID` (the bot owner's Discord user ID, surfaced by `session`
  in the unapproved-server reply). `Version` is injected at **build time via
  ldflags** (`-X .../appconfig.version=...`), not read from env, and supplied as
  a Docker build arg. Its `Load` also pins the **process-wide timezone** from
  `APP_TIMEZONE` (IANA name, default `UTC`) by setting `time.Local`; since the
  logger depends on `AppConfig`, fx runs this before the logger is built, so log
  timestamps render in the chosen zone. The IANA tzdata is embedded in the binary
  (`import _ "time/tzdata"` in `cmd/bot`) so named zones resolve without OS tzdata.
- **`logger`** — `*zap.Logger`, JSON to stderr, `AddStacktrace(ErrorLevel)`,
  level from `LOG_LEVEL` (parsed straight into `zapcore.Level`), `Sync()` on
  shutdown. `New` takes `appconfig.AppConfig`, so **every** log line carries
  `app_name` + `app_version`. fx wiring logs via `fx.WithLogger(fxevent.ZapLogger)`.
- **`db`** — `*pgxpool.Pool` with lifecycle hooks. Assembles the DSN from the
  `POSTGRES_*` parts via `Config.DSN()` (there is no `DATABASE_URL`); the password
  may come from the file-mounted `POSTGRES_PASSWORD_FILE`, which wins — same
  secret pattern as `BOT_TOKEN_FILE`.
- **`migrator`** — applies the embedded goose migrations, then triggers
  `fx.Shutdowner` so the process exits. Loaded **only** in one-shot migrate mode
  (the `--migrate` CLI flag); the long-running bot never includes it and so never
  migrates. The composition root picks the slim migrate graph (db + migrator) vs
  the bot graph via `app.Options(migrate bool)`, fed from the `--migrate` flag in
  `cmd/bot/main.go`. Under Docker a dedicated `migrate` compose service runs
  `--migrate` to completion before the `bot` service starts
  (`depends_on: service_completed_successfully`).
- **`instrumentation`** — ops HTTP server on `INSTRUMENTATION_ADDR` (default
  `:9464`, isolated from the app port so `8080` is free for the future public
  admin API): `/healthz` (liveness), `/readyz` (readiness), `/metrics`
  (Prometheus). `9464` is the OpenTelemetry Prometheus-exporter convention.
  Provides an injectable `*prometheus.Registry` (no global default registry).
  Started early so probes answer during startup. **Readiness is check-based,**
  not a startup flag: subsystems contribute named `ReadinessCheck` probes into
  the `readiness_checks` fx group (`db` pings the pool → `postgres`; `session`
  verifies the gateway is connected — discordgo's `DataReady`, which `Open()`
  returns *before* — → `discord`). `/readyz` runs every probe per request and
  returns 200 only when all pass, so it reflects **live** dependency health
  (goes red again if the DB or gateway later drops), not just "startup finished".
  The package is split one-concern-per-file: `server.go` (HTTP server +
  lifecycle, composes the handlers), `healthz.go` / `readyz.go` / `metrics.go`
  (the three endpoint handlers), `readiness.go` (the `Readiness` aggregator +
  `ReadinessCheck` type), `registry.go` (the Prometheus registry).
- **`session`** — opens the single `discordgo.Session` from `BOT_TOKEN`,
  registers commands **per joined server** on `GuildCreate`, and fans
  `InteractionCreate` events into the command router — but only after the
  `ServerApproval` gate clears the interaction's server. DMs are ignored;
  commands from an unapproved server get an "approval required" reply (mentioning
  `APP_OWNER_DISCORD_ID` when set) instead of being dispatched. Per interaction it
  uses a fresh, bounded context derived from a session-lifetime base context (not
  the fx `OnStart` ctx, which is done once `Start` returns). Also fans guild
  lifecycle events to handlers contributed by other modules via the
  `guild_create` / `guild_delete` fx groups.
  Lifecycle-managed. Uses a `Discord` interface + `Factory` so the manager is
  testable with a fake (no live connection in tests).
- **`servers`** (`internal/discord/servers`) — **core** module that tracks the
  servers (guilds) the bot belongs to. On `GuildCreate` it upserts the `servers`
  row (auto-approving IDs in `APPROVED_SERVER_ID`, promote-only — never demoting
  a manual approval) and logs a `joined` event the first time a server is seen;
  on real `GuildDelete` (not a gateway outage) it logs `removed`. Provides the
  `session.ServerApproval` gate (`IsApproved`) and its guild handlers into the
  session's fx groups.
- **`i18n`** (`internal/i18n`) — **core** module that renders all user-facing
  messages from embedded template bundles, keyed **theme → language → key**. A
  theme is a wording "skin" (shipped: `standard`, `lore`); bundles are
  `locales/<theme>/<lang>.json` (`text/template` strings) embedded via
  `embed.FS`. The `Translator` is a stateless renderer with a fallback chain
  (missing key → same theme's default language → default theme's default
  language → the key itself). The `Localizer` is the handler-facing facade:
  `Render(ctx, serverID, key, data)` resolves the server's theme/language via a
  `Resolver` (provided by `settings`) and renders. Owns the app defaults
  `APP_THEME` / `APP_LANGUAGE`.
- **`settings`** (`internal/settings`) — **core** module owning per-server
  localization. The `server_settings` table holds each server's chosen `theme`
  and `language` (NULL = use the app default). Its `Store` (LRU-cached, like
  `permissions.Store`) implements `i18n.Resolver` (`Resolve` runs on every
  rendered message, so it is cached; writes invalidate the server) and provides
  the **`/settings`** command (`theme` / `language` / `show`, `DefaultDeny` so
  it is owner/admin-gated, with theme/language **choices** sourced from the
  Translator catalog).
- **`commandregistry`** (`internal/discord/registry`) — collects `Command`s and
  `Component`s from feature modules via fx groups, builds the route maps, and
  dispatches three interaction kinds: slash commands (by name), **autocomplete**
  (a `Command`'s optional `Autocomplete` handler, by command name), and **message
  components** (a `Component`'s handler, by the namespace prefix of its CustomID,
  e.g. `base:…`). A `Command` may set **`SubcommandGated`** so the access gate
  keys on the full subcommand path (`AccessKey`) — e.g. `base own register` —
  letting each leaf be granted to different roles; `Policy` still resolves
  `DefaultDeny` per top-level command. The `Responder` also exposes
  `RespondAutocomplete`, `RespondEmbedComponents`, and `UpdateMessage` (in-place
  edit, used for button pagination).
- **feature modules** (`ping`, **`bases`**) — each provides `Command`s (and
  optionally `Component`s) into the groups. They are plain modules (no
  self-gating); which ones load is decided by the composition root.
- **`bases`** (`internal/features/bases`) — the member-bases feature: a single
  SubcommandGated **`/base`** command with three tier groups (`own` / `corp` /
  `member`) × six operations (register, unregister, add/remove extractor,
  add/remove production) plus a paginated, filterable **`list`**. Tier is part of
  the gated path, so per-tier roles are the coarse authorization; on top of that
  **every mutation is ownership-scoped in SQL** (a `WHERE` predicate keyed on
  server + kind + owner), so a forged base id affects zero rows — autocomplete
  pickers are convenience only, never the boundary. Requires the `permissions`
  feature. List pagination keeps its filter context in an in-memory LRU keyed by
  a token in the button CustomID (`base:list:<token>:<page>`).
- **`app`** (composition root, `internal/app`) — assembles the fx option list:
  always-on core modules plus the feature modules selected from `FEATURES`
  (parsed once via `feature.Load()`); `selectFeatures` switches each enabled
  `feature.Name` to its `Module()`.
- **`feature`** (`internal/feature`) — the `Name` enum, a `Feature` catalog
  (each with optional `Requires []Name`), and `Load()` which parses `FEATURES`
  into a validated `[]Name` (unknown names error at parse) and resolves the
  transitive closure of required features. Enabling a feature auto-enables its
  requirements; fx then wires construction order, so selection order is
  irrelevant.

### Key interfaces

- `Command` — name, description, options, handler func. Collected via fx group.
- `Responder` / `Discord` — handlers reply through `Responder`, never touching
  `*discordgo.Session` directly; the session manager uses the `Discord`
  interface (mockable).
- Per-module repository interfaces (e.g. `servers.Repository`), mocked with
  `mockery`.

## Config (decentralized)

`appconfig` provides `AppConfig{ Name, Version }` (and pins `time.Local` from
`APP_TIMEZONE`). There is **no global config aggregator**. Each module defines and loads its own env struct via
`caarlos0/env` inside its own `fx.Module`, aware only of its own keys:

| Module | Env keys |
|---|---|
| `db` | `POSTGRES_HOST` (default `localhost`; compose sets `postgres`), `POSTGRES_PORT` (default `5432`), `POSTGRES_USER`, `POSTGRES_PASSWORD` **or** `POSTGRES_PASSWORD_FILE` (mounted secret; file wins), `POSTGRES_DB`, `POSTGRES_SSLMODE` (default `disable`). `Config.DSN()` assembles the connection string; the app and the `postgres` container share one credential set |
| `logger` | `LOG_LEVEL` (default `info`) |
| `session` | `BOT_TOKEN` **or** `BOT_TOKEN_FILE` (mounted secret; file wins) |
| `servers` | `APPROVED_SERVER_ID` (comma-separated allowlist of auto-approved server IDs; may be empty; promote-only) |
| `instrumentation` | `INSTRUMENTATION_ADDR` (default `:9464`) |
| `i18n` | `APP_THEME` (default `standard`); `APP_LANGUAGE` (default `en`) — app-wide fallback wording theme + language; must match an embedded bundle. `settings` has no env of its own (per-server overrides; defaults from `i18n`) |
| `bases` | `BASES_MEMBER_LIMIT` (default `3`), `BASES_CORP_LIMIT` (default `6`) — live bases per member / per corp; `BASES_EXTRACTOR_LIMIT` (default `4`), `BASES_PRODUCTION_LIMIT` (default `30`) — equipment per base; `BASES_LIST_PAGE_SIZE` (default `8`). Only read when the `bases` feature is enabled |
| `app`/`feature` | `FEATURES` (comma-separated allowlist; unset = all, empty = none) |
| `appconfig` | `APP_NAME` (default `spacecraft-corporation`); `APP_TIMEZONE` (IANA name, default `UTC`, pins `time.Local`); `APP_OWNER_DISCORD_ID` (optional bot-owner Discord ID for the unapproved-server reply); `Version` injected via build-time ldflags |

Feature on/off is the one exception to per-module ownership: it's a composition
concern, owned by the composition root (`internal/app`) via `FEATURES`, not a
per-feature env var.

`.env.example` documents the union for operators; no single Go struct holds it.

## Data model (goose migrations)

- **`servers`** — `id` (**UUIDv7**, app-supplied), `server_id` (unique
  Discord snowflake), `name`, `approved`, `created_at`, `updated_at`. Created
  **first** so the child tables below can reference it.
- **`server_event`** — `id` (**UUIDv7**, app-supplied), `server_id`, `event_type`
  (`joined`\|`removed`), `created_at`. Append-only membership audit, deliberately
  **independent of `servers` (no FK)** so it survives a server row being pruned —
  it keeps the raw snowflake.
- **`permissions`** — `id` (**UUIDv7**, app-supplied), `servers_id`, `command`,
  `role_id`, `created_by_user_id`, `created_at`. Per-server role→command grants
  (any-of) backing the role-based access feature; `unique (servers_id, command,
  role_id)`.
- **`server_settings`** — `id` (**UUIDv7**, app-supplied), `servers_id` (unique),
  `theme`, `language` (both NULL = use app default), `created_at`, `updated_at`.
- **`bases`** — `id` (**UUIDv7**, app-supplied), `servers_id`, `kind`
  (`member`\|`corp`), `owner_user_id` (NULL for corp bases), `name`,
  `sector_name`, `system_code`, `planet_number` (1–10, rendered I–X),
  `created_by_user_id` (differs from owner when a manager registers for a member),
  `created_at`, `updated_at`, `deleted_at` (NULL = live; **soft delete**). Check
  constraints enforce the kind↔owner invariant. Partial indexes on the live rows
  back the listing and the per-member ownership scope.
- **`base_extractors`** / **`base_productions`** — `id` (**UUIDv7**), `bases_id`
  FK, `resource_name` / `item_name`, `created_at`. A base's two independent
  equipment lists (raw-resource extractors vs crafted-item production),
  **hard-deleted** on removal (not audited, unlike bases). One table per
  migration.

`servers_id` is a **`UUID` foreign key to `servers.id`** (`ON DELETE RESTRICT`),
**not** the Discord snowflake — distinguished by name from `servers.server_id`
(the snowflake) and `server_event.server_id`. Handlers and repositories still
work in terms of the snowflake (`i.GuildID`); the repos resolve it in SQL with
`(SELECT id FROM servers WHERE server_id = $1)` on insert and read, so no
servers-UUID is threaded through the Go code. The `servers` row always exists
before any child row (upserted on `GuildCreate`); a missing one makes the
subselect `NULL` and the insert fail loudly (NOT NULL / FK violation).
  Per-server localization choice read by the `i18n` Localizer (via `settings`).

New tables use **UUIDv7** primary keys **generated by the application**
(`uuid.NewV7()` from `github.com/google/uuid`), not the database — the `id`
columns have **no `DEFAULT`**, so every INSERT must supply the id. v7 embeds a
timestamp, so ids sort in creation order (better index locality and pagination
than random v4).

Timestamp columns are **`TIMESTAMP`** (without time zone), not `TIMESTAMPTZ`,
and are **supplied by the application**, not the database — they have **no
`DEFAULT`**. Every INSERT passes `time.Now()`, whose location is `time.Local`
(pinned to `APP_TIMEZONE`); pgx stores that wall clock verbatim, so values match
the configured zone (and the log timestamps). The default is intentionally
omitted so a forgotten timestamp fails loudly (NOT NULL violation) instead of
silently recording a wrong-zone `now()`. Updates advance only `updated_at`
(`servers`), preserving `created_at`.

**Creating migrations:** always scaffold via **goose** (never hand-name the
file), and use **timestamped** names (goose's default), not sequential numbers:
`make dev.migration name=create_foo` →
`internal/migrator/migrations/<YYYYMMDDhhmmss>_create_foo.sql`. Timestamps avoid
the version-number collisions sequential numbering causes when branches add
migrations in parallel. **One table per migration file** — each `CREATE TABLE`
gets its own migration (e.g. `create_servers` and `create_server_event` are
separate files), so a table's schema has a single, self-contained history and
can be reviewed/reverted independently. Goose is baked into the dev image
(`GOOSE_VERSION`, kept in sync with go.mod); migrations still run via the
`migrator` module on `--migrate`. Don't rename an already-applied migration — its
filename is its recorded version, so a rename re-runs it as a "new" one.

## Command flow (`/ping`)

Interaction → `session` receives `InteractionCreate` → **server must be approved**
(`servers` gate); if not, the bot replies "approval required" (mentioning the
owner) instead of dispatching → router looks up `ping` → handler replies with an
embed showing two latencies: **handle latency** (time inside the bot, read from
the dispatcher's start instant via `registry.Elapsed` — the same reference as
the `discord_command_duration_seconds` histogram) and **round-trip latency**
(`now` minus the interaction's creation time, decoded from its ID snowflake).
`/ping` is a stateless probe — it persists nothing. Commands are registered per
joined server on `GuildCreate`.

## Build & run (Docker)

The application **builds and runs in Docker** — no host Go toolchain required.
A single multi-stage `Dockerfile` with named targets:

- **`build`** stage (`golang` image): compiles a static binary with the version
  baked in via ldflags,
  `go build -ldflags "-X <module>/internal/appconfig.version=${APP_VERSION}" -o /bot ./cmd/bot`.
  `APP_VERSION` is a build arg, required for prod, defaulting to `dev` if unset.
- **`prod`** stage (minimal, e.g. `gcr.io/distroless/static` or `alpine`):
  copies only the binary; runs as non-root. Built and pushed to Docker Hub by CI
  (the release workflow) on a version tag; the prod compose file pulls it.
- **`dev`** stage (`golang` image + `air` + `delve`): used by the dev compose
  file for hot reload and step debugging (see below). Built **without**
  optimizations/inlining (`-gcflags="all=-N -l"`) so the debugger works.

### Two compose files

- **`docker-compose.yml` (prod)** — **pulls** the published image
  `kweezls/spacecraft-corporation:${IMAGE_TAG:-latest}` from Docker Hub (it does
  **not** build; CI does — see CI/CD below). `IMAGE_TAG` selects the release tag
  to run (default `latest`); `pull_policy: always` re-fetches on `up`. `migrate`
  and `bot` share that one pulled image. Runs `postgres` (named volume) →
  `migrate` (one-shot `--migrate`, runs to completion) → `bot` (waits on
  `migrate` via `service_completed_successfully`). No source mount, no debugger.
- **`docker-compose.dev.yml` (local dev)** — builds the `dev` target. Mounts the
  source tree into the container and runs **`air`** (`air -c .air.toml`) for hot
  reload. A one-shot `migrate` service (`go run ./cmd/bot --migrate` over the
  mounted source) applies migrations to completion before the `bot` service
  starts — the air-run bot no longer migrates, so re-run `migrate` after adding a
  migration. Postgres port published to the host. Exposes the **delve** debug
  port `2345` for a step debugger.

Prod is `docker compose up`; dev is
`docker compose -f docker-compose.dev.yml up`.

### Hot reload (air)

`.air.toml` at repo root drives local hot reload: watches `**/*.go`, rebuilds on
change, and (for debugging) launches the binary under delve rather than running
it directly — see below. The build bakes a version tag via ldflags the same way
prod does, read from the `APP_VERSION` env var (default `dev`); run a custom tag with
`APP_VERSION=mytag docker compose -f docker-compose.dev.yml up`. `send_interrupt` is
on so a rebuild sends SIGINT (not SIGKILL), letting fx `OnStop` hooks run. The
dev image pins `GOCACHE` to `/home/dev/.cache/go-build` — outside the
bind-mounted `/src` and air's `tmp_dir` — so air's cleanup never wipes the build
cache and rebuilds stay incremental.

### Step debugging (delve)

In the `dev` container, `air`'s run command launches the rebuilt binary under
**delve** in headless multi-client mode:

```
dlv exec ./tmp/bot --headless --listen=:2345 --api-version=2 \
    --accept-multiclient --continue
```

Port `2345` is published by `docker-compose.dev.yml`. Attach from GoLand via a
**Go Remote** run config (host `localhost`, port `2345`). On each source change
air rebuilds and relaunches under delve, so the debugger reconnects to the new
process.

## Observability (probes & metrics)

The `instrumentation` module exposes, on `INSTRUMENTATION_ADDR` (default `:9464`):
- `GET /healthz` — liveness; always `200 ok` once the server is listening.
- `GET /readyz` — readiness; runs every contributed `ReadinessCheck` per request
  and returns `503 starting` unless **all** pass, then `200 ready`. Reflects live
  dependency health (DB pool ping + Discord gateway connected), so it can flip
  back to `503` if a dependency later drops — it is not a one-shot startup flag.
- `GET /metrics` — Prometheus exposition (Go runtime collectors + app metrics).

App metrics register into the injected `*prometheus.Registry`. Command calls are
instrumented centrally by the registry in `Dispatch` (so features don't each need
their own metrics): `discord_command_total{command="..."}` counts calls and
`discord_command_duration_seconds{command="..."}` is a latency histogram (default
buckets) over the handler run. The 2.5s/5s buckets straddle Discord's ~3s
interaction deadline, so a rising p99 there foreshadows "Unknown interaction"
(10062) errors.

**Metrics naming convention** (`prometheus.CounterOpts` etc.):
- `Namespace` = module name / area (e.g. `discord`)
- `Subsystem` = the func / command / contextual feature (e.g. `command`)
- `Name` = what we collect (e.g. `total`)
- If a metric is genuinely shared across modules with no single owner, leave
  `Namespace` and `Subsystem` empty and use only `Name`.

**Metrics live in their own file.** Declare metric constructors in a dedicated
`metrics.go` within the owning package (see `internal/discord/registry/
metrics.go`); don't mix metric declarations into files holding other types.

Kubernetes probes (kubelet does the HTTP GET, so the image needs no shell):
```yaml
livenessProbe:
  httpGet: { path: /healthz, port: 9464 }
readinessProbe:
  httpGet: { path: /readyz, port: 9464 }
startupProbe:
  httpGet: { path: /healthz, port: 9464 }
  failureThreshold: 30   # allow slow first start (DB + Discord connect)
  periodSeconds: 2
```

## CI/CD & linting

- **Test + lint** live in a **reusable workflow** (`.github/workflows/checks.yml`,
  `on: workflow_call`): `go test ./... -race` (with a `postgres` service
  container for DB-backed tests) and `golangci-lint` (config in `.golangci.yml`).
  Defined once so every caller runs the identical suite.
- **GitHub Actions** (`.github/workflows/ci.yml`) runs on push/PR: calls the
  `checks` reusable workflow, plus a Docker build of the `prod` target to catch
  build breakage.
- **Release** (`.github/workflows/release.yml`) runs on a `vX.X.X` git tag and is
  **gated on `checks`** (the Docker job `needs:` it, so a tag on a red commit
  fails before publishing). It builds the `prod` target **multi-arch
  (`linux/amd64,linux/arm64`)** with `docker/build-push-action` and pushes a
  manifest to Docker Hub as `kweezls/spacecraft-corporation` tagged `X.X.X` (the `v`
  stripped) — plus `latest` for non-prerelease tags (`flavor: latest=auto`) —
  with OCI `org.opencontainers.image.*` labels from `docker/metadata-action`.
  `APP_VERSION` is baked from the bare semver. Needs the `DOCKERHUB_USERNAME` /
  `DOCKERHUB_TOKEN` repo secrets. The prod compose then pulls this image by
  `IMAGE_TAG`.
- **Lint:** `golangci-lint` is the standard linter; config lives in
  `.golangci.yml`. Run it locally before pushing.
- **DB-backed tests** (`internal/testdb`) connect via **`TEST_DATABASE_URL`**
  (the admin DSN). It has no default and DB tests **fail — never skip — when it
  is unset**, so a missing config can't hide a regression behind a green check.
  Each testify suite gets its **own** `CREATE DATABASE`d database (migrated in
  `SetupSuite`, tables truncated between tests, `DROP`ped in `TearDownSuite`), so
  package test binaries run in parallel with no shared state or locking. CI
  points `TEST_DATABASE_URL` at a `postgres` service container; locally, set it to
  a reachable Postgres (e.g. a throwaway `spacecraft_test` DB) — the per-suite
  databases are created beside it. Needs no Docker at test time (unlike
  testcontainers), which suits environments that forbid Docker-in-Docker.

## Project layout

```
cmd/bot/main.go              # app.Options() -> fx.New(...).Run()
internal/app/                # composition root: core + selected features
internal/feature/            # Name enum + FEATURES parsing/validation
internal/appconfig/
internal/logger/
internal/db/
internal/migrator/           # + migrations/*.sql (embedded)
internal/instrumentation/    # liveness/readiness probes + /metrics
internal/discord/session/    # single-session manager + discordgo wrapper
internal/discord/registry/   # command/component registry + router + Command type
internal/discord/servers/    # server tracking + approval gate + event log
internal/features/ping/
internal/features/bases/     # /base: registry + command tree + list pagination
.mockery.yaml         # mock generation config
.air.toml            # hot-reload config (local dev)
.golangci.yml        # linter config
Dockerfile           # multi-stage: build / prod / dev targets
docker-compose.yml       # prod
docker-compose.dev.yml   # local dev (air + delve, port 2345)
.env.example
.github/workflows/ci.yml # lint + test on push/PR
```

## First deliverable scope (YAGNI)

`/ping` end to end: the `session` module opens the bot from `BOT_TOKEN`; `/ping`
replies with an embed reporting the bot's handle and round-trip latency (a
stateless probe — no persistence).

**Not** in the first slice: sharding and any game-data feature modules beyond
`ping`. (The server allowlist / approval flow — the `servers` module — has since
been added. Dev/prod compose, air hot reload,
delve debugging, golangci-lint, the GitHub Actions CI workflow, and the health
probes/metrics *are* part of the first slice — project infrastructure, not
features.)

## Conventions

- **Prefer existing packages over custom code.** Before implementing any
  non-trivial functionality yourself, check whether a well-maintained library
  already solves it (start with the chosen stack, then the wider Go ecosystem).
  Evaluate fit by maintenance, stars/activity, API quality, and license. Only
  hand-roll when no suitable package exists, the dependency is disproportionate
  to the need, or it would compromise a core constraint — and say why.
- TDD: write tests first. Regenerate mocks with `mockery` when interfaces change.
- Create DB migrations with **goose**, timestamped (never hand-named or
  sequentially numbered): `make dev.migration name=<snake_case>` — see Data model.
- Every module exposes `func Module() fx.Option` (not a `var`), in its own
  `module.go`, that returns its `fx.Module(...)`. Each module adds
  `logger.Decorate("<name>")` as the first option, so its log lines carry a
  `module=<name>` field (scoped to that module via `fx.Decorate`; lazy, so it's
  a no-op for modules that don't log). **Exception:** `appconfig` does not
  decorate — `logger` depends on it (importing `logger` there would be a cycle)
  and it doesn't log. Combined with the logger's app fields, a typical line
  carries `app_name`, `app_version`, and `module`. Modules do not self-gate.
  Which feature modules load is
  decided once by the composition root (`internal/app`) from `FEATURES`; core
  modules always load. This is a startup decision — fx builds the graph once at
  `fx.New` and cannot add/remove modules at runtime.
- New feature = (1) add a `feature.Name` const + a `catalog()` entry (with any
  `Requires`) in `internal/feature`, (2) add a `Module() fx.Option` under
  `internal/features/` contributing `Command`s via the fx group, (3) add a
  `case` for it in `internal/app`'s `selectFeatures`.
- Never touch `*discordgo.Session` directly in handlers — reply via the
  `registry.Responder`; the session manager uses the `Discord` interface.
- **No hardcoded user-facing strings.** Every message a user sees is a key in the
  `i18n` bundles (`internal/i18n/locales/<theme>/<lang>.json`), rendered via the
  injected `*i18n.Localizer` (`loc.Render(ctx, i.GuildID, "key", data)`). Add the
  key to **every** shipped theme/language file. Internal errors (returned to the
  dispatcher, logged) are not user-facing and stay as plain Go strings.
- "server" is the domain term for a Discord guild; map discordgo's `guild`
  fields onto `server`-named values at the boundary.
- `BOT_TOKEN` is a secret — never committed (`.env` is gitignored), never in the
  database or logs. For prod prefer **`BOT_TOKEN_FILE`** pointing at a mounted
  secret file (Docker `secrets:` / K8s secret volume): env reads the file's
  contents (caarlos0/env `,file` option), trimmed of whitespace. Files avoid the
  env-var leak paths (`docker inspect`, `/proc/<pid>/environ`, child-process
  inheritance). The `_FILE` var wins over `BOT_TOKEN` if both are set.