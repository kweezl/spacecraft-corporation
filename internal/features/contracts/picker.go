package contracts

import (
	"context"
	"errors"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// The gamedata picker: the shared search-then-pick flow behind every "choose a
// game object" input (contract items, template items, delivery locations, and
// legacy-item linking). A search modal collects a query; runPick searches the
// catalog in the server's language and either applies the single hit directly,
// or transforms the console message IN PLACE into a pick page — a string-select
// of the top hits plus a Back button — so the user's focus never leaves the
// message they were working in (a modal may not open from a modal submit, so
// the pick can't be a second modal). Choosing a hit (or backing out) transforms
// the same message into the destination view.

// pickMaxHits caps the hits offered in the pick select — well under Discord's
// 25-option limit, keeps the ephemeral tidy.
const pickMaxHits = 10

// pickDest routes an applied pick to its destination write + re-render. The
// value rides in the pick select's CustomID.
type pickDest string

const (
	pickContractItem pickDest = "ci" // AddItemByID on a contract
	pickTemplateItem pickDest = "ti" // AddTemplateItem on a template
	pickTemplateLoc  pickDest = "tl" // SetTemplateLocation on a template
	pickContractLoc  pickDest = "cl" // SetDeliveryLocation on a contract
	pickItemLink     pickDest = "il" // LinkItemGDID on a legacy free-text item
)

// kind is the gamedata index the destination picks from.
func (d pickDest) kind() gamedata.Kind {
	if d == pickTemplateLoc || d == pickContractLoc {
		return gamedata.KindSpaceObject
	}
	return gamedata.KindItem
}

// needsQty says whether the destination takes a quantity — those picks route
// through the quantity modal; the rest apply immediately.
func (d pickDest) needsQty() bool {
	return d == pickContractItem || d == pickTemplateItem
}

// GameSearch is the gamedata autocomplete search the picker runs. Implemented
// by *gamedata.Searcher; an interface so handler tests can fake hits.
type GameSearch interface {
	Search(kind gamedata.Kind, lang i18n.Language, query string, limit int) ([]gamedata.Hit, error)
}

// LangResolver resolves the server's wording theme + language — the picker
// searches and snapshots names in the server's language. Implemented by
// *settings.Store (the same Resolve the Localizer renders through).
type LangResolver interface {
	Resolve(ctx context.Context, serverID uuid.UUID) (theme string, lang i18n.Language)
}

// lang is the server's resolved content language.
func (h *Feature) lang(ctx context.Context, serverID uuid.UUID) i18n.Language {
	_, lang := h.langs.Resolve(ctx, serverID)
	return lang
}

// catalogFor resolves a stored gamedata_version to its catalog, falling back to
// the latest loaded version for an unknown/empty one (e.g. a version no longer
// in GAMEDATA_VERSIONS). May return nil only when no versions are loaded at all;
// callers nil-guard.
func (h *Feature) catalogFor(version string) *gamedata.Catalog {
	if version != "" {
		if cat, ok := h.reg.Version(version); ok {
			return cat
		}
	}
	return h.reg.Latest()
}

// itemDisplay renders a gamedata item as "<emoji> Name" in the server's
// language: the emoji token resolves via the item's icon name (absent icon or
// emoji degrades to the bare name), the name via the stamped catalog version.
func (h *Feature) itemDisplay(ctx context.Context, serverID uuid.UUID, gdid, version string) string {
	cat := h.catalogFor(version)
	if cat == nil {
		return gdid
	}
	id := gamedata.GDID(gdid)
	name := cat.Name(id, h.lang(ctx, serverID))
	if name == "" {
		name = gdid
	}
	if token := h.emojiToken(cat.IconName(id)); token != "" {
		return token + " " + name
	}
	return name
}

// itemName is itemDisplay without the emoji token — the localized name for
// plain-text surfaces (the CSV export). Free-text items (no GDID) and unknown
// gdids / nil catalog fall back to the stored name snapshot.
func (h *Feature) itemName(ctx context.Context, serverID uuid.UUID, it Item) string {
	if it.GDID == "" {
		return it.Name
	}
	cat := h.catalogFor(it.GDVersion)
	if cat == nil {
		return it.Name
	}
	if name := cat.Name(gamedata.GDID(it.GDID), h.lang(ctx, serverID)); name != "" {
		return name
	}
	return it.Name
}

// spaceObjectDisplay renders a gamedata space object's localized name.
func (h *Feature) spaceObjectDisplay(ctx context.Context, serverID uuid.UUID, gdid, version string) string {
	cat := h.catalogFor(version)
	if cat == nil {
		return gdid
	}
	name := cat.SpaceObjectName(gamedata.GDID(gdid), h.lang(ctx, serverID))
	if name == "" {
		return gdid
	}
	return name
}

// emojiToken resolves an icon name to a ready-to-send emoji token, "" when the
// icon or the emoji store is absent (tests, or the emoji sync not done yet).
func (h *Feature) emojiToken(iconName string) string {
	if iconName == "" || h.emo == nil {
		return ""
	}
	token, _ := h.emo.Format(iconName)
	return token
}

// runPick executes a search-modal submit: search the catalog in the server's
// language and dispatch on the hit count — nothing found (ephemeral notice, the
// console keeps its view so the user just retries), exactly one for a qty-less
// destination (apply + re-render in place), or otherwise transform the console
// message into the pick page. A single hit for an ITEM destination still goes
// through the pick page: its quantity is asked in a modal after the pick, and a
// modal may not open from this (modal-submit) interaction — the one-option
// select is the bridge.
func (h *Feature) runPick(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest pickDest, targetID uuid.UUID, query string) error {
	hits, err := h.search.Search(dest.kind(), h.lang(ctx, serverID), query, pickMaxHits)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	// Excluded categories never surface as item hits (they can't be contract
	// requirements; applyPick rejects them as the hard boundary).
	if dest.kind() == gamedata.KindItem {
		kept := hits[:0]
		for _, hit := range hits {
			if h.itemPickable(hit.ID) {
				kept = append(kept, hit)
			}
		}
		hits = kept
	}
	if len(hits) == 0 {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.pick_none", map[string]any{"Query": query}))
	}
	if len(hits) == 1 && !dest.needsQty() {
		return h.applyPick(ctx, r, i, serverID, dest, targetID, string(hits[0].ID), 0, true)
	}

	options := make([]discordgo.SelectMenuOption, 0, len(hits))
	for _, hit := range hits {
		options = append(options, discordgo.SelectMenuOption{
			Label: truncate(hit.Name, 100),
			Value: string(hit.ID),
			// The item's icon renders inline in the dropdown — select options
			// support custom emojis, the one Discord surface where icons and a
			// pick list combine.
			Emoji: h.optionEmoji(hit.ID),
		})
	}
	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.pick_title", map[string]any{"Query": query})},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
			MenuType:    discordgo.StringSelectMenu,
			CustomID:    buildID(segPick, string(dest), targetID.String()),
			Placeholder: h.loc.Render(ctx, serverID, "contracts.console.pick_placeholder", nil),
			Options:     options,
		}}},
		divider(),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_search", nil),
				Style:    discordgo.PrimaryButton,
				CustomID: pickSearchID(dest, targetID),
			},
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: pickBackID(dest, targetID),
			},
		}},
	}
	// Update IN PLACE: the console message the modal was opened from becomes the
	// pick page, so the user's focus stays where they were working. Choosing a
	// hit (or Back) transforms the same message into the destination view;
	// Search reopens the query modal to refine without leaving the page.
	return h.respondView(i, r, []discordgo.MessageComponent{discordgo.Container{Components: inner}}, true)
}

// optionEmoji resolves a catalog item's icon (from the latest loaded catalog) to
// a select-option emoji — the search/browse pick lists, which always work off the
// latest catalog.
func (h *Feature) optionEmoji(gdid gamedata.GDID) *discordgo.ComponentEmoji {
	return h.optionEmojiFor(gdid, "")
}

// optionEmojiFor resolves a catalog item's icon to a select-option emoji using
// the catalog stamped by version (empty = latest), nil when the item has no icon
// or the emoji store doesn't carry it (space objects have no icons, so location
// picks always render plain). Used by the op-modal item options, which key off
// the item's stored gamedata version.
func (h *Feature) optionEmojiFor(gdid gamedata.GDID, version string) *discordgo.ComponentEmoji {
	if h.emo == nil {
		return nil
	}
	cat := h.catalogFor(version)
	if cat == nil {
		return nil
	}
	iconName := cat.IconName(gdid)
	if iconName == "" {
		return nil
	}
	id, ok := h.emo.ID(iconName)
	if !ok {
		return nil
	}
	return &discordgo.ComponentEmoji{Name: iconName, ID: id}
}

// pickBackID is the navigation CustomID that abandons a pick and returns to the
// destination's view.
func pickBackID(dest pickDest, targetID uuid.UUID) string {
	switch dest {
	case pickItemLink:
		return buildID(segIRow, targetID.String())
	case pickTemplateItem, pickTemplateLoc:
		return buildID(segTView, targetID.String(), "0")
	default: // contract item / location
		return buildID(segView, targetID.String())
	}
}

// pickSearchID is the CustomID that reopens the destination's search modal, so
// refining a query needs no navigation. Only the searching destinations appear
// here — locations are picked from the modal-free browser (browse.go) and never
// reach the search pick page.
func pickSearchID(dest pickDest, targetID uuid.UUID) string {
	if dest == pickItemLink {
		return buildID(segILink, targetID.String())
	}
	// contract / template item: the browse page's search opener.
	return buildID(segBrowseSearch, string(dest), targetID.String())
}

// handlePickSelect handles the choice made in the pick select: item
// destinations continue to the quantity modal (this is a component interaction,
// so a modal may open), the rest apply immediately. segPick is not in
// gatedSegments (it isn't a fixed console button), so it re-checks the manager
// key here.
func (h *Feature) handlePickSelect(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	if len(parts) < 2 {
		return fmt.Errorf("contracts: malformed pick id %v", parts)
	}
	dest := pickDest(parts[0])
	targetID, ok := argUUID(parts, 1)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}

	allowed, err := h.authorizedKey(ctx, i, serverID, keyManage)
	if err != nil {
		return fmt.Errorf("contracts: authorize %s: %w", keyManage, err)
	}
	if !allowed {
		return h.reply(ctx, r, i, serverID, "contracts.console.denied", nil)
	}

	values := i.MessageComponentData().Values
	if len(values) != 1 {
		return fmt.Errorf("contracts: pick select expects one value, got %d", len(values))
	}
	if dest.needsQty() {
		return h.openPickQtyModal(ctx, r, i, serverID, dest, targetID, values[0])
	}
	return h.applyPick(ctx, r, i, serverID, dest, targetID, values[0], 0, true)
}

// itemAliases collects every name a catalog item is known by — its localized
// name in each game language plus the gdid itself — so duplicate checks catch a
// pre-gamedata free-text item regardless of the language it was typed in.
func (h *Feature) itemAliases(gdid, version string) []string {
	cat := h.catalogFor(version)
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

// applyPick writes the picked game object to its destination and re-renders the
// destination view. New links are stamped with the latest loaded catalog
// version; contract items also snapshot the localized name (the public panel's
// identity).
func (h *Feature) applyPick(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest pickDest, targetID uuid.UUID, gdid string, qty int, update bool) error {
	cat := h.reg.Latest()
	if cat == nil {
		return h.consoleErr(ctx, r, i, serverID, errors.New("contracts: no gamedata versions loaded"))
	}
	version := cat.Version()
	actor := invokerID(i)

	// The hard boundary for excluded categories: the browser and the search both
	// hide them, but the gdid arrives via a CustomID, which can be forged.
	if dest.kind() == gamedata.KindItem && !h.itemPickable(gamedata.GDID(gdid)) {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.item_excluded", nil))
	}

	switch dest {
	case pickContractItem:
		name := cat.Name(gamedata.GDID(gdid), h.lang(ctx, serverID))
		if name == "" {
			name = gdid
		}
		err := h.repo.AddItemByID(ctx, serverID, targetID, name, gdid, version, h.itemAliases(gdid, version), qty, h.cfg.MaxItems, actor)
		if errors.Is(err, ErrMaxItems) {
			return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.max_items", map[string]any{"Limit": h.cfg.MaxItems}))
		}
		if err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
		return h.renderContractView(ctx, r, i, serverID, targetID, 0, update)
	case pickItemLink:
		if _, err := h.repo.LinkItemGDID(ctx, serverID, targetID, gdid, version, h.itemAliases(gdid, version), actor); err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
		return h.renderItemView(ctx, r, i, serverID, targetID, 0, update)
	case pickContractLoc:
		if err := h.repo.SetDeliveryLocation(ctx, serverID, targetID, gdid, version, actor); err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
		return h.renderContractView(ctx, r, i, serverID, targetID, 0, update)
	case pickTemplateItem:
		err := h.tpls.AddTemplateItem(ctx, serverID, targetID, gdid, version, qty, h.cfg.MaxItems, actor)
		if errors.Is(err, ErrMaxItems) {
			return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.max_items", map[string]any{"Limit": h.cfg.MaxItems}))
		}
		if err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
		return h.renderTemplateEditView(ctx, r, i, serverID, targetID, 0, update)
	case pickTemplateLoc:
		if err := h.tpls.SetTemplateLocation(ctx, serverID, targetID, gdid, version, actor); err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
		return h.renderTemplateEditView(ctx, r, i, serverID, targetID, 0, update)
	default:
		return fmt.Errorf("contracts: unknown pick dest %q", dest)
	}
}
