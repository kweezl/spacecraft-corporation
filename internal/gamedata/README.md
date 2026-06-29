# gamedata

Core module exposing the compiled-in SpaceCraft **game reference data** (items,
item categories, contract templates, space objects) as a **versioned, read-only**
`Registry` of `Catalog`s. Nothing is parsed at runtime — the data is generated
pure-Go literals under `db/v*`; item **icons are emitted into the emoji module's
assets** so every feature can render them.

## Layout

```
schema/            dependency-free value types (Item, Contract, …, Lang)
db/v1/             generated package v1 (the base layer) + snapshot.json
gen/               the generator (dev-only, go:generate)
catalog.go         Catalog: version-scoped, parent-chain lookups
registry.go        Registry: loads versions, Version(name) / Latest()
registry_gen.go    generated: defined version → package vars
```

## Versioning model

Versions are **manually cut** (`v1`, `v2`, …) and form a **parent chain**: a
newer version stores only its **delta** (changed/added entries + removed item
ids) over its parent, so unchanged data is shared, not duplicated. A lookup
checks the layer, honors its removals, then falls back to the parent.

Why versions exist: stored links (e.g. a contract's item ids) are stamped with a
`gamedata_version`. When a game update **breaks** compatibility (an item or
contract id disappears), you **cut a new version** instead of mutating the old
one — links stamped with the older version keep resolving against it. A
**backward-compatible** update (only additions/field tweaks) just overwrites the
current top version in place.

Only the **highest** version is mutable; cutting a newer one freezes it.

`GAMEDATA_VERSIONS` (env) is the allowlist of versions to load (ancestors load
automatically; unknown names are warned and skipped). Unset = all defined.
`Registry.Latest()` is what new links should stamp.

## Regenerating

Needs the host Go toolchain and a clone of the **public** resources repo
([kweezl/spacecraft-resources](https://github.com/kweezl/spacecraft-resources)):

```sh
export GAMEDATA_SOURCE=/path/to/spacecraft-resources/generated
make gamedata.gen                      # regenerate v1 (go generate)
make gamedata.gen version=v2 parent=v1 # cut a new layer after a breaking update
```

The generator parses the JSON, applies the **exclusion rules** (drops Knowledge /
QuestItem / uncategorized / Scrap / decorative items — **except** any id a
contract or space object references, which is always kept), emits the Go +
`snapshot.json`, copies kept item icons into `internal/emoji/assets/`
(additive — the asset set is the union across versions), and prints a
diff/verdict (backward-compatible vs breaking) so you know whether to overwrite
or cut a new version. The generated Go is committed; **CI never runs the
generator.**
