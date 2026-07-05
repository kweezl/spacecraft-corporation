package contracts

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// The gamedata picker/browser mechanics live in the shared internal/discord/
// gamepick package (h.pick, built in New with the five destinations from
// destinations.go). This file keeps the feature-local search/lang interfaces fx
// provides, the two small catalog helpers other console code still calls
// directly, and thin delegators so existing call sites (embed/view/panel/tasks)
// compile unchanged.

// GameSearch is the gamedata autocomplete search the picker runs. Implemented by
// *gamedata.Searcher; an interface so handler tests can fake hits. Kept as a
// contracts-local type (fx provides it here) so it doesn't collide with another
// feature providing the same shared type — it converts implicitly to
// gamepick.GameSearch.
type GameSearch interface {
	Search(kind gamedata.Kind, lang i18n.Language, query string, limit int) ([]gamedata.Hit, error)
}

// LangResolver resolves the server's wording theme + language. Implemented by
// *settings.Store; contracts-local for the same fx reason as GameSearch.
type LangResolver interface {
	Resolve(ctx context.Context, serverID uuid.UUID) (theme string, lang i18n.Language)
}

// lang is the server's resolved content language.
func (h *Feature) lang(ctx context.Context, serverID uuid.UUID) i18n.Language {
	_, lang := h.langs.Resolve(ctx, serverID)
	return lang
}

// catalogFor resolves a stored gamedata_version to its catalog, falling back to
// the latest loaded version for an unknown/empty one. Nil only when no versions
// are loaded at all; callers nil-guard.
func (h *Feature) catalogFor(version string) *gamedata.Catalog {
	if version != "" {
		if cat, ok := h.reg.Version(version); ok {
			return cat
		}
	}
	return h.reg.Latest()
}

// --- thin delegators to the shared picker (byte-identical CustomIDs) ---------

func (h *Feature) itemDisplay(ctx context.Context, serverID uuid.UUID, gdid, version string) string {
	return h.pick.ItemDisplay(ctx, serverID, gdid, version)
}

func (h *Feature) itemName(ctx context.Context, serverID uuid.UUID, it Item) string {
	return h.pick.LocalizedItemName(ctx, serverID, it.GDID, it.GDVersion, it.Name)
}

func (h *Feature) spaceObjectDisplay(ctx context.Context, serverID uuid.UUID, gdid, version string) string {
	return h.pick.SpaceObjectDisplay(ctx, serverID, gdid, version)
}

func (h *Feature) optionEmoji(gdid gamedata.GDID) *discordgo.ComponentEmoji {
	return h.pick.OptionEmoji(gdid)
}

func (h *Feature) optionEmojiFor(gdid gamedata.GDID, version string) *discordgo.ComponentEmoji {
	return h.pick.OptionEmojiFor(gdid, version)
}

// runPick executes a search-modal submit for the given destination.
func (h *Feature) runPick(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest pickDest, targetID uuid.UUID, query string) error {
	return h.pick.RunPick(ctx, r, i, serverID, string(dest), targetID, query)
}

// renderBrowseCategories opens the category browser for an item destination.
func (h *Feature) renderBrowseCategories(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest pickDest, targetID uuid.UUID) error {
	return h.pick.RenderBrowse(ctx, r, i, serverID, string(dest), targetID)
}

// renderLocationBrowser opens the delivery-location picker for a location
// destination.
func (h *Feature) renderLocationBrowser(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest pickDest, targetID uuid.UUID) error {
	return h.pick.RenderLocation(ctx, r, i, serverID, string(dest), targetID)
}
