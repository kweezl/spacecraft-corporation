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

## Bot model (single bot)

One bot identity that you own. It runs **a single `discordgo` session** from
`BOT_TOKEN` (env) and is added to other Discord **servers** via an OAuth2 invite
link — no per-server tokens, no token storage. To gate who can add it, turn off
"Public Bot" in the Developer Portal (only the app owner can invite) and/or keep
a server allowlist. Slash-command registration scope is **configurable**:
`server` (instant, to `DEV_SERVER_ID`) or `global` (~1h propagation).

Terminology: **"server"** is our domain term for a Discord guild. discordgo's API
still says `guild` (e.g. `i.GuildID`); we read those into `server`-named values.

## fx modules

- **`appconfig`** — provides shared `AppConfig{ Name, Version }` only. Knows
  nothing about other modules' settings. `Name` comes from the `APP_NAME` env
  var (default `spacecraft-corporation`). `Version` is injected at **build time via
  ldflags** (`-X .../appconfig.version=...`), not read from env, and supplied as
  a Docker build arg.
- **`logger`** — `*zap.Logger`, JSON to stderr, `AddStacktrace(ErrorLevel)`,
  level from `LOG_LEVEL` (parsed straight into `zapcore.Level`), `Sync()` on
  shutdown. `New` takes `appconfig.AppConfig`, so **every** log line carries
  `app_name` + `app_version`. fx wiring logs via `fx.WithLogger(fxevent.ZapLogger)`.
- **`db`** — `*pgxpool.Pool` with lifecycle hooks. Owns `DATABASE_URL`.
- **`migrator`** — runs goose migrations on startup before the session serves.
- **`health`** — ops HTTP server on `HEALTH_ADDR` (default `:9464`, isolated
  from the app port so `8080` is free for the future public admin API):
  `/healthz` (liveness), `/readyz` (readiness), `/metrics` (Prometheus).
  `9464` is the OpenTelemetry Prometheus-exporter convention. Provides a
  `Readiness` flag and an injectable `*prometheus.Registry` (no global default
  registry). Started early so probes answer during startup. Readiness goes green
  via `MarkReady`, appended **last** by the composition root, so `/readyz`
  returns 200 only after every module's `OnStart` (incl. session connect) ran.
- **`session`** — opens the single `discordgo.Session` from `BOT_TOKEN`,
  registers commands at the configured scope, and fans `InteractionCreate`
  events into the command router. Lifecycle-managed. Uses a `Discord` interface +
  `Factory` so the manager is testable with a fake (no live connection in tests).
- **`commandregistry`** — collects `Command`s from feature modules via an fx
  group, builds the route map, dispatches interactions to handlers.
- **feature modules** (first: **`ping`**) — each provides `Command`s into the
  group. They are plain modules (no self-gating); which ones load is decided by
  the composition root.
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
- Per-module repository interfaces (e.g. `ping.Repository`), mocked with
  `mockery`.

## Config (decentralized)

`appconfig` provides only `AppConfig{ Name, Version }`. There is **no global
config aggregator**. Each module defines and loads its own env struct via
`caarlos0/env` inside its own `fx.Module`, aware only of its own keys:

| Module | Env keys |
|---|---|
| `db` | `DATABASE_URL` (under Docker, compose assembles it from `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB` so the app and the `postgres` container share one credential set) |
| `logger` | `LOG_LEVEL` (default `info`) |
| `session` | `BOT_TOKEN` **or** `BOT_TOKEN_FILE` (mounted secret; file wins), `COMMAND_SCOPE` (`server`\|`global`, default `server`), `DEV_SERVER_ID` |
| `health` | `HEALTH_ADDR` (default `:9464`) |
| `app`/`feature` | `FEATURES` (comma-separated allowlist; unset = all, empty = none) |
| `appconfig` | `APP_NAME` (default `spacecraft-corporation`); `Version` injected via build-time ldflags |

Feature on/off is the one exception to per-module ownership: it's a composition
concern, owned by the composition root (`internal/app`) via `FEATURES`, not a
per-feature env var.

`.env.example` documents the union for operators; no single Go struct holds it.

## Data model (goose migrations)

- **`ping_log`** — `id`, `server_id`, `user_id`, `created_at`.

## Command flow (`/ping`)

Interaction → `session` receives `InteractionCreate` → router looks up `ping` →
handler calls `Repository.Record(serverID, userID)` → replies `pong (#N)`.
Registration scope follows `COMMAND_SCOPE`.

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
it directly — see below. The build bakes a version tag via ldflags the same way
prod does, read from the `VERSION` env var (default `dev`); run a custom tag with
`VERSION=mytag docker compose -f docker-compose.dev.yml up`. `send_interrupt` is
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

The `health` module exposes, on `HEALTH_ADDR` (default `:9464`):
- `GET /healthz` — liveness; always `200 ok` once the server is listening.
- `GET /readyz` — readiness; `503 starting` until the app fully starts, then
  `200 ready`. Green only after **all** modules' `OnStart` ran.
- `GET /metrics` — Prometheus exposition (Go runtime collectors + app metrics).

App metrics register into the injected `*prometheus.Registry`. Command calls are
counted centrally by the registry as `discord_command_total{command="..."}` (so
features don't each need a counter).

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
internal/health/             # liveness/readiness probes + /metrics
internal/discord/session/    # single-session manager + discordgo wrapper
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

`/ping` end to end: goose migrations create `ping_log`; the `session` module
opens the bot from `BOT_TOKEN`; `/ping` records to Postgres and replies
`pong (#N)`.

**Not** in the first slice: sharding, a server allowlist / approval flow, and
any game-data feature modules beyond `ping`. (Dev/prod compose, air hot reload,
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
- "server" is the domain term for a Discord guild; map discordgo's `guild`
  fields onto `server`-named values at the boundary.
- `BOT_TOKEN` is a secret — never committed (`.env` is gitignored), never in the
  database or logs. For prod prefer **`BOT_TOKEN_FILE`** pointing at a mounted
  secret file (Docker `secrets:` / K8s secret volume): env reads the file's
  contents (caarlos0/env `,file` option), trimmed of whitespace. Files avoid the
  env-var leak paths (`docker inspect`, `/proc/<pid>/environ`, child-process
  inheritance). The `_FILE` var wins over `BOT_TOKEN` if both are set.