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
  nothing about other modules' settings.
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
  group. Disabled modules contribute nothing.

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
| `ping` | `FEATURE_PING_ENABLED` |
| `appconfig` | `APP_NAME`, `APP_VERSION` (or build-time ldflags) |

`.env.example` documents the union for operators; no single Go struct holds it.

## Data model (goose migrations)

- **`bot_tokens`** — `id`, `guild_id`, `token` (AES-GCM ciphertext), `enabled`, `created_at`.
- **`ping_log`** — `id`, `guild_id`, `user_id`, `created_at`.

## Command flow (`/ping`)

Interaction → `sessionmanager` receives `InteractionCreate` on owning session →
router looks up `ping` → handler calls `PingRepository.Record(guildID, userID)`
→ replies `pong`. Registration scope follows `COMMAND_SCOPE`.

## Project layout

```
cmd/bot/main.go              # fx.New(...).Run()
internal/appconfig/
internal/logger/
internal/db/
internal/migrator/           # + migrations/*.sql (embedded)
internal/crypto/
internal/discord/session/    # SessionManager + Session wrapper
internal/discord/registry/   # command registry + router + Command type
internal/features/ping/
.mockery.yaml  docker-compose.yml  Dockerfile  .env.example
```

## First deliverable scope (YAGNI)

`/ping` end to end: goose migrations create `bot_tokens` + `ping_log`;
`sessionmanager` starts sessions from DB tokens (one bootstrap token suffices),
decrypting via `crypto`; `/ping` records to Postgres and replies.

**Not** in the first slice: token-admin/registration UX, key rotation, sharding,
GitHub Actions, and any game-data feature modules beyond `ping`. Multi-tenancy
lives in the boundaries now; only `ping` is built on top.

## Conventions

- TDD: write tests first. Regenerate mocks with `mockery` when interfaces change.
- New feature = new `fx.Module` under `internal/features/`, gated by a
  `FEATURE_<NAME>_ENABLED` env in its own config struct, contributing `Command`s
  via the fx group.
- Never touch `*discordgo.Session` directly in handlers — go through the
  `Session` wrapper.
- Never store secrets in plaintext; bot tokens are AES-GCM encrypted.