# gamedata

Core fx module exposing the compiled-in SpaceCraft **game reference data** —
items, item categories, contract templates, space objects — as a **versioned,
read-only** `Registry` of `Catalog`s.

Nothing is parsed at runtime. The data is **generated pure-Go literals** under
`db/v*` (a build-time `gen` tool reads the public resources repo and bakes the
structs), so there is no database, no I/O, and no readiness probe — the
`Registry` is ready the moment fx builds it. Kept item **icons** are emitted
into the `emoji` module's assets, so every feature can render an item with its
in-game art.

## Layout

```
schema/            dependency-free value types (Item, Category, Contract, …)
db/v1/             generated package v1 (the base layer) + snapshot.json
gen/               the generator (dev-only, go:generate) — never run in CI
module.go          fx.Module: provides the Registry + Searcher
registry.go        Registry: loads versions, Version(name) / Latest() / Loaded()
registry_gen.go    generated: defined version → package vars (definedSources)
catalog.go         Catalog: version-scoped, parent-chain lookups + enumerators
contract.go        ContractView: resolved/localized contract lines + rewards
search.go          Searcher: bleve substring autocomplete, per category
config.go          GAMEDATA_VERSIONS allowlist parsing
gamedata.go        re-exported value types + DefaultLang
```

## Consuming the data

Inject the `*gamedata.Registry`, pick a `*Catalog` by version, and look entities
up by their `GDID`. The value types are re-exported from `gamedata` (aliases of
`schema`), so consumers import only this package.

```go
type handler struct{ data *gamedata.Registry }

// A stored link carries the version it was stamped with.
cat, ok := h.data.Version(link.GamedataVersion)
if !ok { /* version no longer loaded */ }

item, ok := cat.Item(link.ItemID)          // parent-chain lookup, removals honored
name := cat.Name(item.ID, lang)            // localized, falls back to DefaultLang
token, _ := emojiStore.Format(item.IconName) // render the item's icon

// New links stamp the newest loaded version.
fresh := h.data.Latest().Version()
```

### `Registry`

- `Version(name) (*Catalog, bool)` — the catalog for a version name as stored on
  a link.
- `Latest() *Catalog` — newest loaded version; what **new** links should stamp.
  `nil` only when no versions are loaded (`GAMEDATA_VERSIONS` set-but-empty).
- `Loaded() []string` — loaded version names, oldest first.

### `Catalog`

One version's read-only view. Maps are never mutated after construction, so
every method is **safe for concurrent use**. Each lookup checks this layer,
honors this layer's item removals, then falls back to the parent.

- `Item` / `Category` / `Contract` / `SpaceObject` `(id) (T, bool)` — resolve by
  id along the parent chain. A removed item id reports `false` even if a parent
  still defines it.
- `Name` / `Desc` `(id, lang) string` — an item's localized name/description, or
  `""`; a missing translation falls back to `DefaultLang` (`en`).
- `CategoryName` / `ContractName` / `SpaceObjectName` `(id, lang) string`,
  `FactionName(code, lang) string` — localized names for the other entities (same
  fallback). Contracts/factions/space objects are **name-only** in the data.
- `IconName(id) string` — the canonical emoji name for an item's icon (resolve
  to a sendable token via the `emoji` `Store`), or `""`.
- `Items()` / `Contracts()` / `SpaceObjects()` / `FactionCodes()` — effective,
  flattened, sorted enumerations (for listing/indexing, not hot paths).
- `Version() string` — this catalog's version name.

### `ContractView`

`cat.ContractView(id, lang)` binds a contract to the catalog + language so its
lines resolve and localize: `RequiredItems()` / `RewardItems()` return
`ContractItem`s (item + localized `Name` + `Qty`/`Count`, currencies excluded
from rewards), and `RewardCorpoCredits` / `RewardCorpoReputation` /
`RewardCorpoLicensePoints(factor)` return the corporation-currency payouts scaled
by a bonus factor.

### Search (autocomplete)

Inject the `*gamedata.Searcher` for fast substring autocomplete over a **single
category at a time** — results are never mixed across kinds:

```go
hits, err := searcher.Search(gamedata.KindContract, lang, "ingot", 25)
// []gamedata.Hit{ {ID, Name}, … } — contract titles containing "ingot", prefix matches first
```

`Search(kind, lang, query, limit)` substring-matches the localized name for the
kind (`KindItem` / `KindContract` / `KindFaction` / `KindSpaceObject`) against the
**latest** loaded version, case-insensitively (Latin + Cyrillic), prefix matches
ranked first. It builds one in-memory **bleve** index per `(kind, language)`
lazily on first use and caches it (the data is immutable), so only the
languages/kinds actually queried are ever indexed; the module closes them on
shutdown. Duplicate names (many contracts share a title) are disambiguated by the
consumer using `Catalog` fields, not the `Searcher`.

### Value types (`schema`)

`GDID` is the game-data object id every entity carries and the key the tables
join on (references between objects are `GDID`s too). `Item` deliberately does
**not** store its name/description — those live in per-language string tables and
resolve via `Catalog.Name` / `Catalog.Desc`. `Category` nodes form a tree via
`Parent`. `Contract` holds `[]RequestItem` (deliver) and `[]RewardItem`
(receive), whose ids resolve against the **same version's** catalog. The
language code is the app-wide `i18n.Language`, shared so the i18n/settings layer
and the game data agree on codes.

## Versioning model

Versions are **manually cut** (`v1`, `v2`, …) and form a **parent chain**: a
newer version stores only its **delta** (changed/added entries + removed item
ids) over its parent, so unchanged data is shared, not duplicated.

Why versions exist — **link stability**. A stored link (e.g. a contract's item
ids) is stamped with a `gamedata_version`. When a game update **breaks**
compatibility (an item or contract id disappears), you **cut a new version** that
overlays the old one instead of mutating it — links stamped with the older
version keep resolving against it. A **backward-compatible** update (only
additions / field tweaks) just **overwrites the current top version in place**.
Only the **highest** version is mutable; cutting a newer one freezes it.

## Config

| Env | Meaning |
|---|---|
| `GAMEDATA_VERSIONS` | Comma-separated allowlist of versions to load (e.g. `v1,v2`). A version's ancestors load automatically; a listed-but-undefined version is warned and skipped. **Unset** = load every defined version; **set-but-empty** = none. |
| `GAMEDATA_SOURCE` | **Generator only** (dev/maintainer, *not* read by the bot) — the `generated/` dir of the [spacecraft-resources](https://github.com/kweezl/spacecraft-resources) repo, used by `make gamedata.gen`. |

The module builds the `Registry` eagerly (`fx.Invoke`) at startup, so the
loaded-versions log line and any unknown-version warnings surface immediately,
and an undefined parent fails fast rather than on first use.

## Regenerating

Needs the host Go toolchain and a clone of the **public** resources repo. The
generated Go is **committed**; **CI never runs the generator.**

```sh
export GAMEDATA_SOURCE=/path/to/spacecraft-resources/generated
make gamedata.gen                       # regenerate v1 (the base layer)
make gamedata.gen version=v2 parent=v1  # cut a new layer after a breaking update
```

The generator (`go run ./gen -version vN [-parent vM]`):

1. Parses the resources JSON and applies the **exclusion rules** — drops
   Knowledge / QuestItem / uncategorized / Scrap / decorative items, **except**
   any id a contract or space object references, which is always kept.
2. Emits the Go literals (`db/vN/*_gen.go`) — for a delta layer, only the
   changed/added entries plus the removed-item ids — including the per-language
   name tables (items, categories, contracts, factions, space objects) for all
   eight game languages, and regenerates `registry_gen.go`.
3. Writes a committed `snapshot.json` per version — the effective (flattened)
   dataset that is the diff basis for the next regeneration.
4. Copies kept item icons into `internal/emoji/assets/` (additive: the asset set
   is the **union** across versions).
5. Prints a diff and a **verdict** — `backward-compatible` (safe to overwrite
   this version in place) vs `BREAKING` (an item/contract was removed → cut a new
   version with `-parent`).