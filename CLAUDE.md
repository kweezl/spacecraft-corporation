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
- **Migrations:** `goose`, SQL embedded via `embed.FS`, run on startup
- **Config:** `caarlos0/env`, decentralized per module (see Config)
- **Logging:** `uber-go/zap` — JSON encoder, stderr, stacktraces at error level and above
- **Testing:** TDD. Mocks via `mockery`. Assertions via `testify/assert` + `testify/require`
- **Hosting:** Docker Compose (bot + postgres). Code on GitHub.

## Multi-tenancy (core constraint)

Genuine multi-tenant hosting: one OS process manages **multiple `discordgo`
sessions, one per bot token**, with tokens loaded from Postgres. Bot tokens are
**encrypted at rest with AES-GCM** (key from `ENCRYPTION_KEY`). Slash-command
registration scope is **configurable** (`guild` vs `global`).

## fx modules

- **`appconfig`** — provides shared `AppConfig{ Name, Version }` only. Knows
  nothing about other modules' settings. `Name` comes from the `APP_NAME` env
  var (default `spacecraft-cadet`). `Version` is injected at **build time via
  ldflags** (`-X .../appconfig.version=...`), not read from env, and supplied as
  a Docker build arg.
- **`logger`** — `*zap.Logger`, JSON to stderr, `AddStacktrace(ErrorLevel)`,
  level from `LOG_LEVEL`, `Sync()` on shutdown. fx wiring logs via
  `fx.WithLogger(fxevent.ZapLogger)`.
- **`db`** — `*pgxpool.Pool` with lifecycle hooks. Owns `DATABASE_URL`.
- **`migrator`** — runs goose migrations on startup before sessions serve.
- **`crypto`** — AES-GCM encrypt/decrypt helper (key from `ENCRYPTION_KEY`).
- **`sessionmanager`** — loads enabled tokens from DB (decrypts via `crypto`),
  opens one `discordgo.Session` per token, registers commands per session at the
  configured scope, fans `InteractionCreate` events into the command router.
  Lifecycle-managed.
- **`commandregistry`** — collects `Command`s from feature modules via an fx
  group, builds the route map, dispatches interactions to handlers.
- **feature modules** (first: **`ping`**) — each provides `Command`s into the
  group. They are plain modules (no self-gating); which ones load is decided by
  the composition root.
- **`app`** (composition root, `internal/app`) — assembles the fx option list:
  always-on core modules plus the feature modules selected from `FEATURES`
  (parsed once via `feature.Load()`). Holds the `feature.Name → Module()` map.
- **`feature`** (`internal/feature`) — the `Name` enum + `Load()` that parses
  `FEATURES` into a validated `[]Name` (unknown names error at parse).

### Key interfaces

- `Command` — name, description, options, handler func. Collected via fx group.
- `Session` — thin wrapper over `*discordgo.Session`; handlers/tests never touch
  discordgo directly (mockable).
- Per-module repository interfaces (`TokenRepository`, `PingRepository`, …),
  mocked with `mockery`.

## Config (decentralized)

`appconfig` provides only `AppConfig{ Name, Version }`. There is **no global
config aggregator**. Each module defines and loads its own env struct via
`caarlos0/env` inside its own `fx.Module`, aware only of its own keys:

| Module | Env keys |
|---|---|
| `db` | `DATABASE_URL` |
| `logger` | `LOG_LEVEL` (default `info`) |
| `sessionmanager` | `COMMAND_SCOPE` (`guild`\|`global`), `DEV_GUILD_ID`, `ENCRYPTION_KEY`, `BOOTSTRAP_BOT_TOKEN` (optional, seeds first token) |
| `crypto` | `ENCRYPTION_KEY` |
| `app`/`feature` | `FEATURES` (comma-separated allowlist; unset = all, empty = none) |
| `appconfig` | `APP_NAME` (default `spacecraft-cadet`); `Version` injected via build-time ldflags |

Feature on/off is the one exception to per-module ownership: it's a composition
concern, owned by the composition root (`internal/app`) via `FEATURES`, not a
per-feature env var.

`.env.example` documents the union for operators; no single Go struct holds it.

## Data model (goose migrations)

- **`bot_tokens`** — `id`, `guild_id`, `token` (AES-GCM ciphertext), `enabled`, `created_at`.
- **`ping_log`** — `id`, `guild_id`, `user_id`, `created_at`.

## Command flow (`/ping`)

Interaction → `sessionmanager` receives `InteractionCreate` on owning session →
router looks up `ping` → handler calls `PingRepository.Record(guildID, userID)`
→ replies `pong`. Registration scope follows `COMMAND_SCOPE`.

## Build & run (Docker)

The application **builds and runs in Docker** — no host Go toolchain required.
A single multi-stage `Dockerfile` with named targets:

- **`build`** stage (`golang` image): compiles a static binary with the version
  baked in via ldflags,
  `go build -ldflags "-X <module>/internal/appconfig.version=${VERSION}" -o /bot ./cmd/bot`.
  `VERSION` is a build arg, required for prod, defaulting to `dev` if unset.
- **`prod`** stage (minimal, e.g. `gcr.io/distroless/static` or `alpine`):
  copies only the binary; runs as non-root. Used by the prod compose file.
- **`dev`** stage (`golang` image + `air` + `delve`): used by the dev compose
  file for hot reload and step debugging (see below). Built **without**
  optimizations/inlining (`-gcflags="all=-N -l"`) so the debugger works.

### Two compose files

- **`docker-compose.yml` (prod)** — builds the image at the `prod` target with
  `build.args.VERSION` (from a shell/CI variable). Runs `bot` + `postgres`
  (named volume). No source mount, no debugger. Migrations run on bot startup
  via the `migrator` module.
- **`docker-compose.dev.yml` (local dev)** — builds the `dev` target. Mounts the
  source tree into the container and runs **`air`** (`air -c .air.toml`) for hot
  reload. Postgres port published to the host. Exposes the **delve** debug port
  `2345` for a step debugger.

Prod is `docker compose up`; dev is
`docker compose -f docker-compose.dev.yml up`.

### Hot reload (air)

`.air.toml` at repo root drives local hot reload: watches `**/*.go`, rebuilds on
change, and (for debugging) launches the binary under delve rather than running
it directly — see below.

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

## CI/CD & linting

- **GitHub Actions** (`.github/workflows/ci.yml`) runs on push/PR:
  - `golangci-lint run` (config in `.golangci.yml`),
  - `go test ./...` (with a `postgres` service container for DB-backed tests),
  - optionally a Docker build of the `prod` target to catch build breakage.
- **Lint:** `golangci-lint` is the standard linter; config lives in
  `.golangci.yml`. Run it locally before pushing.

## Project layout

```
cmd/bot/main.go              # app.Options() -> fx.New(...).Run()
internal/app/                # composition root: core + selected features
internal/feature/            # Name enum + FEATURES parsing/validation
internal/appconfig/
internal/logger/
internal/db/
internal/migrator/           # + migrations/*.sql (embedded)
internal/crypto/
internal/discord/session/    # SessionManager + Session wrapper
internal/discord/registry/   # command registry + router + Command type
internal/features/ping/
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

`/ping` end to end: goose migrations create `bot_tokens` + `ping_log`;
`sessionmanager` starts sessions from DB tokens (one bootstrap token suffices),
decrypting via `crypto`; `/ping` records to Postgres and replies.

**Not** in the first slice: token-admin/registration UX, key rotation, sharding,
and any game-data feature modules beyond `ping`. Multi-tenancy lives in the
boundaries now; only `ping` is built on top. (Dev/prod compose, air hot reload,
delve debugging, golangci-lint, and the GitHub Actions CI workflow *are* part of
the first slice — they're project infrastructure, not features.)

## Conventions

- **Prefer existing packages over custom code.** Before implementing any
  non-trivial functionality yourself, check whether a well-maintained library
  already solves it (start with the chosen stack, then the wider Go ecosystem).
  Evaluate fit by maintenance, stars/activity, API quality, and license. Only
  hand-roll when no suitable package exists, the dependency is disproportionate
  to the need, or it would compromise a core constraint — and say why.
- TDD: write tests first. Regenerate mocks with `mockery` when interfaces change.
- Every module exposes `func Module() fx.Option` (not a `var`) that returns its
  `fx.Module(...)`. Modules do not self-gate. Which feature modules load is
  decided once by the composition root (`internal/app`) from `FEATURES`; core
  modules always load. This is a startup decision — fx builds the graph once at
  `fx.New` and cannot add/remove modules at runtime.
- New feature = (1) add a `feature.Name` const in `internal/feature`, (2) add a
  `Module() fx.Option` under `internal/features/` contributing `Command`s via
  the fx group, (3) register `Name → Module` in `internal/app`'s
  `featureModules` map.
- Never touch `*discordgo.Session` directly in handlers — go through the
  `Session` wrapper.
- Never store secrets in plaintext; bot tokens are AES-GCM encrypted.