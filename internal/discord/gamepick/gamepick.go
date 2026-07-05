// Package gamepick is the shared "choose a game object" flow behind every
// gamedata-linked input: a search-then-pick over the catalog plus a zero-typing
// category browser and a space-object location picker. It was extracted from the
// contracts feature so contracts and supply share one implementation.
//
// A Picker is stateless: every CustomID it emits carries only the destination
// code + target id (+ transient view params), so a click acts on exactly that
// object. The feature owns the destinations — each Destination injects the
// feature-specific write+render (Apply), access re-check (Authorize), navigation
// ids (BackID/SearchID), search-modal opener, and location current/clear hooks —
// while the Picker owns the generic mechanics (search, option building, paging,
// the quantity modal, the browsers).
//
// CustomID bytes are part of the contract: the Picker builds ids as
// "<Prefix>:<seg>[:<part>...]" with fixed segment literals (pick/brw/brwi/brwsub/
// brws/m_bqty/lbrw/lclr), so a feature that passes Prefix "contract" emits the
// exact ids the pre-extraction contracts code did (pinned by a grammar test).
package gamepick

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/emoji"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// GameSearch is the gamedata autocomplete search the picker runs. Implemented by
// *gamedata.Searcher; an interface so handler tests can fake hits. Each feature
// keeps its own local copy of this interface (fx provides it per feature) — the
// Picker only needs the method set.
type GameSearch interface {
	Search(kind gamedata.Kind, lang i18n.Language, query string, limit int) ([]gamedata.Hit, error)
}

// LangResolver resolves the server's wording theme + language — the picker
// searches and snapshots names in the server's language. Implemented by
// *settings.Store.
type LangResolver interface {
	Resolve(ctx context.Context, serverID uuid.UUID) (theme string, lang i18n.Language)
}

// ErrResponder renders a repository/gamepick error to the interaction (the
// feature's console error mapper). It must always acknowledge the interaction.
type ErrResponder func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, err error) error

// Picked is a resolved pick: the chosen gdid stamped with the latest catalog
// version, plus (for item destinations) the localized name snapshot and the
// cross-language aliases a destination may persist.
type Picked struct {
	GDID    string
	Version string   // latest loaded catalog version at pick time
	Name    string   // localized name snapshot (server language); gdid fallback
	Aliases []string // every known name + the gdid (item destinations only)
}

// Applier writes a resolved pick to its destination and re-renders the view.
type Applier func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID, p Picked, qty int, update bool) error

// Authorizer re-checks access before an apply. proceed=false means it already
// responded (e.g. a "denied" reply); err carries only unexpected failures. A nil
// Authorizer is treated as always-allowed (ownership enforced in SQL instead).
type Authorizer func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID) (proceed bool, err error)

// NavID builds a feature-owned navigation CustomID (Back / Search) for a target.
type NavID func(targetID uuid.UUID) string

// ModalOpener opens the destination's query modal (from the browser's Search
// button). The feature builds the modal with its own submit segment.
type ModalOpener func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID) error

// CurrentFn loads a location destination's stored gdid ("" = unset).
type CurrentFn func(ctx context.Context, serverID, targetID uuid.UUID) (string, error)

// ClearFn clears a location destination and re-renders its view.
type ClearFn func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID) error

// Destination is one place a pick can be applied. Its Code rides in every
// CustomID; Kind selects the gamedata index; NeedsQty routes items through the
// quantity modal; Browse enables the category browser (item destinations only).
type Destination struct {
	Code     string
	Kind     gamedata.Kind
	NeedsQty bool
	Browse   bool

	Authorize Authorizer  // nil = always allowed
	Apply     Applier     // required
	BackID    NavID       // required — the pick page's Back button
	SearchID  NavID       // non-browse destinations only (the pick page's Search)
	OpenModal ModalOpener // browse destinations — the browser's Search opener

	Current CurrentFn // location destinations
	Clear   ClearFn   // location destinations
}

// Config wires a Picker to the feature's core dependencies. Prefix is the
// component namespace (byte-identical to the feature's, e.g. "contract"); Keys is
// the i18n key prefix for the picker's own messages (e.g. "contracts.console").
type Config struct {
	Prefix string
	Keys   string
	Loc    *i18n.Localizer
	Reg    *gamedata.Registry
	Emo    *emoji.Store
	Search GameSearch
	Langs  LangResolver
	Log    *zap.Logger
	// OnError renders an error to the interaction (the feature's console error
	// mapper). NotFound is the feature's not-found sentinel, passed to OnError for
	// malformed/forged CustomIDs so the feature renders its own "not found".
	OnError  ErrResponder
	NotFound error
}

// Picker is the extracted picker/browser. Build one per feature with New.
type Picker struct {
	cfg   Config
	dests map[string]Destination
}

// New builds a Picker for the given destinations (keyed by Code).
func New(cfg Config, dests ...Destination) *Picker {
	m := make(map[string]Destination, len(dests))
	for _, d := range dests {
		m[d.Code] = d
	}
	return &Picker{cfg: cfg, dests: m}
}

// dest looks up a registered destination by code.
func (p *Picker) dest(code string) (Destination, bool) {
	d, ok := p.dests[code]
	return d, ok
}

// key renders a picker i18n key (Keys + "." + name) for the server.
func (p *Picker) key(ctx context.Context, serverID uuid.UUID, name string, data map[string]any) string {
	return p.cfg.Loc.Render(ctx, serverID, p.cfg.Keys+"."+name, data)
}

// langOf is the server's resolved content language.
func (p *Picker) langOf(ctx context.Context, serverID uuid.UUID) i18n.Language {
	_, lang := p.cfg.Langs.Resolve(ctx, serverID)
	return lang
}

// notFound responds via the feature's error mapper with its not-found sentinel.
func (p *Picker) notFound(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	return p.cfg.OnError(ctx, r, i, serverID, p.cfg.NotFound)
}

// --- catalog / display helpers (feature delegators call these) ---------------

// CatalogFor resolves a stored gamedata_version to its catalog, falling back to
// the latest loaded version for an unknown/empty one. Nil only when no versions
// are loaded at all; callers nil-guard.
func (p *Picker) CatalogFor(version string) *gamedata.Catalog {
	if version != "" {
		if cat, ok := p.cfg.Reg.Version(version); ok {
			return cat
		}
	}
	return p.cfg.Reg.Latest()
}

// ItemDisplay renders a gamedata item as "<emoji> Name" in the server's
// language: the emoji token resolves via the item's icon name (absent icon or
// emoji degrades to the bare name), the name via the stamped catalog version.
func (p *Picker) ItemDisplay(ctx context.Context, serverID uuid.UUID, gdid, version string) string {
	cat := p.CatalogFor(version)
	if cat == nil {
		return gdid
	}
	id := gamedata.GDID(gdid)
	name := cat.Name(id, p.langOf(ctx, serverID))
	if name == "" {
		name = gdid
	}
	if token := p.EmojiToken(cat.IconName(id)); token != "" {
		return token + " " + name
	}
	return name
}

// LocalizedItemName is ItemDisplay without the emoji token — the localized name
// for plain-text surfaces (the CSV export). A missing gdid, nil catalog, or
// unknown id falls back to the provided name snapshot.
func (p *Picker) LocalizedItemName(ctx context.Context, serverID uuid.UUID, gdid, version, fallback string) string {
	if gdid == "" {
		return fallback
	}
	cat := p.CatalogFor(version)
	if cat == nil {
		return fallback
	}
	if name := cat.Name(gamedata.GDID(gdid), p.langOf(ctx, serverID)); name != "" {
		return name
	}
	return fallback
}

// SpaceObjectDisplay renders a gamedata space object's localized name.
func (p *Picker) SpaceObjectDisplay(ctx context.Context, serverID uuid.UUID, gdid, version string) string {
	cat := p.CatalogFor(version)
	if cat == nil {
		return gdid
	}
	name := cat.SpaceObjectName(gamedata.GDID(gdid), p.langOf(ctx, serverID))
	if name == "" {
		return gdid
	}
	return name
}

// EmojiToken resolves an icon name to a ready-to-send emoji token, "" when the
// icon or the emoji store is absent (tests, or the emoji sync not done yet).
func (p *Picker) EmojiToken(iconName string) string {
	if iconName == "" || p.cfg.Emo == nil {
		return ""
	}
	token, _ := p.cfg.Emo.Format(iconName)
	return token
}

// OptionEmoji resolves a catalog item's icon (from the latest catalog) to a
// select-option emoji — the search/browse pick lists work off the latest catalog.
func (p *Picker) OptionEmoji(gdid gamedata.GDID) *discordgo.ComponentEmoji {
	return p.OptionEmojiFor(gdid, "")
}

// OptionEmojiFor resolves a catalog item's icon to a select-option emoji using
// the catalog stamped by version (empty = latest), nil when the item has no icon
// or the emoji store doesn't carry it.
func (p *Picker) OptionEmojiFor(gdid gamedata.GDID, version string) *discordgo.ComponentEmoji {
	if p.cfg.Emo == nil {
		return nil
	}
	cat := p.CatalogFor(version)
	if cat == nil {
		return nil
	}
	iconName := cat.IconName(gdid)
	if iconName == "" {
		return nil
	}
	id, ok := p.cfg.Emo.ID(iconName)
	if !ok {
		return nil
	}
	return &discordgo.ComponentEmoji{Name: iconName, ID: id}
}

// Aliases collects every name a catalog item is known by — its localized name in
// each game language plus the gdid itself — so duplicate checks catch a
// pre-gamedata free-text item regardless of the language it was typed in.
func (p *Picker) Aliases(gdid, version string) []string {
	cat := p.CatalogFor(version)
	if cat == nil {
		return []string{gdid}
	}
	id := gamedata.GDID(gdid)
	aliases := make([]string, 0, len(i18n.KnownLanguages())+1)
	aliases = append(aliases, gdid)
	for _, lang := range i18n.KnownLanguages() {
		if name := cat.Name(id, lang); name != "" {
			aliases = append(aliases, name)
		}
	}
	return aliases
}

// excludedItemCategories are display categories whose items can never be picked
// as a haulable requirement (constructions, data, and blueprints). They are
// hidden from the browser, dropped from search hits, and — the real boundary —
// rejected when a pick is applied, so a forged CustomID can't smuggle one in.
var excludedItemCategories = map[string]bool{
	"BaseBuilding": true,
	"BeaconData":   true,
	"Blueprint":    true,
}

// Pickable reports whether a catalog item may be picked as an item requirement:
// it must exist in the latest catalog and not belong to an excluded category.
func (p *Picker) Pickable(gdid gamedata.GDID) bool {
	cat := p.cfg.Reg.Latest()
	if cat == nil {
		return false
	}
	it, ok := cat.Item(gdid)
	return ok && !excludedItemCategories[it.DisplayCategory]
}
