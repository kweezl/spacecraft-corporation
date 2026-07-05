package contracts

import (
	"context"
	"fmt"
	"sort"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
)

// The category browser: the zero-typing item picker behind "Add item". The
// console message transforms into a category list; picking a category shows its
// items ordered by name (with icons, paged); picking an item prompts for the
// quantity in a one-field modal, and the submit applies. A Search button on the
// category page opens the query modal for type-first users — that path converges
// on the same pick → quantity steps. Only the item destinations (contract item,
// template item) browse; locations and legacy-item linking stay search-only
// (space objects have no categories, linking prefills the old name).
//
// Both views are plain navigation (mutations are re-checked when the quantity
// submits — see submitBrowseQty), and both selects post back to their own view's
// CustomID: an interaction WITH a value is a choice, one without (the Back /
// pager buttons) is a render.

// browsePageSize is the item-list page size — Discord's select-option cap.
const browsePageSize = 25

// excludedItemCategories are display categories whose items can never be a
// contract requirement (constructions, data, and blueprints aren't haulable
// cargo). They are hidden from the browser, dropped from search hits, and —
// the real boundary — rejected when a pick is applied, so a forged CustomID
// can't smuggle one in either.
var excludedItemCategories = map[string]bool{
	"BaseBuilding": true,
	"BeaconData":   true,
	"Blueprint":    true,
}

// itemPickable reports whether a catalog item may be required by a contract:
// it must exist in the latest catalog and not belong to an excluded category.
func (h *Feature) itemPickable(gdid gamedata.GDID) bool {
	cat := h.reg.Latest()
	if cat == nil {
		return false
	}
	it, ok := cat.Item(gdid)
	return ok && !excludedItemCategories[it.DisplayCategory]
}

// subAll is the subcategory select's "no filter" option value (an option value
// may not be empty).
const subAll = "-"

// categorySelectRow builds the category dropdown shared by both browser views:
// distinct display categories with item counts, ordered by localized name, the
// current one (if any) pre-selected. Choosing a category always renders its
// first item page, so the row doubles as the switcher on the item page — no
// Back round-trip to change categories.
func (h *Feature) categorySelectRow(ctx context.Context, serverID uuid.UUID, cat *gamedata.Catalog, dest pickDest, targetID uuid.UUID, selected string) discordgo.MessageComponent {
	lang := h.lang(ctx, serverID)

	// ~400 items — trivially cheap per render.
	counts := map[string]int{}
	type catRow struct{ id, name string }
	var cats []catRow
	for _, it := range cat.Items() {
		if it.DisplayCategory == "" || excludedItemCategories[it.DisplayCategory] {
			continue
		}
		if counts[it.DisplayCategory] == 0 {
			name := cat.CategoryName(gamedata.GDID(it.DisplayCategory), lang)
			if name == "" {
				name = it.DisplayCategory
			}
			cats = append(cats, catRow{id: it.DisplayCategory, name: name})
		}
		counts[it.DisplayCategory]++
	}
	sort.Slice(cats, func(a, b int) bool { return cats[a].name < cats[b].name })

	options := make([]discordgo.SelectMenuOption, 0, len(cats))
	for _, c := range cats {
		options = append(options, discordgo.SelectMenuOption{
			Label: truncate(c.name, 100),
			Value: c.id,
			// The item count under each category signals up front when a
			// category spans several 25-option pages (Discord's select cap).
			Description: h.loc.Render(ctx, serverID, "contracts.console.browse_cat_count", map[string]any{"Items": counts[c.id]}),
			Default:     c.id == selected,
		})
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    buildID(segBrowse, string(dest), targetID.String()),
		Placeholder: h.loc.Render(ctx, serverID, "contracts.console.browse_placeholder", nil),
		Options:     options,
	}}}
}

// renderBrowseCategories transforms the console message into the category list
// for an item pick destination (pickContractItem / pickTemplateItem).
func (h *Feature) renderBrowseCategories(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest pickDest, targetID uuid.UUID) error {
	cat := h.reg.Latest()
	if cat == nil {
		return h.consoleErr(ctx, r, i, serverID, fmt.Errorf("contracts: no gamedata versions loaded"))
	}
	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.browse_title", nil)},
		h.categorySelectRow(ctx, serverID, cat, dest, targetID, ""),
		divider(),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_search", nil),
				Style:    discordgo.PrimaryButton,
				CustomID: buildID(segBrowseSearch, string(dest), targetID.String()),
			},
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: pickBackID(dest, targetID),
			},
		}},
	}
	return h.respondView(i, r, []discordgo.MessageComponent{discordgo.Container{Components: inner}}, true)
}

// renderBrowseItems transforms the console message into one page of a
// category's items, ordered by localized name. sub ("" = all) narrows the page
// to one subcategory; the optional subcategory dropdown appears whenever the
// category distinguishes more than one.
func (h *Feature) renderBrowseItems(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest pickDest, targetID uuid.UUID, category string, page int, sub string) error {
	cat := h.reg.Latest()
	if cat == nil {
		return h.consoleErr(ctx, r, i, serverID, fmt.Errorf("contracts: no gamedata versions loaded"))
	}
	lang := h.lang(ctx, serverID)

	type itemRow struct{ id, name string }
	var items []itemRow
	subSeen := map[string]bool{}
	var subIDs []string
	for _, it := range cat.Items() {
		// The exclusion re-applies here so a forged category id can't page
		// through hidden items.
		if it.DisplayCategory != category || excludedItemCategories[it.DisplayCategory] {
			continue
		}
		if it.Subcategory != "" && !subSeen[it.Subcategory] {
			subSeen[it.Subcategory] = true
			subIDs = append(subIDs, it.Subcategory)
		}
		if sub != "" && it.Subcategory != sub {
			continue
		}
		name := cat.Name(it.ID, lang)
		if name == "" {
			name = string(it.ID)
		}
		items = append(items, itemRow{id: string(it.ID), name: name})
	}
	sort.Slice(items, func(a, b int) bool { return items[a].name < items[b].name })

	totalPages := (len(items) + browsePageSize - 1) / browsePageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	if page < 0 {
		page = 0
	}
	start := page * browsePageSize
	end := start + browsePageSize
	if end > len(items) {
		end = len(items)
	}

	catName := cat.CategoryName(gamedata.GDID(category), lang)
	if catName == "" {
		catName = category
	}
	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.browse_items_title", map[string]any{
			"Category": catName, "Page": page + 1, "Pages": totalPages,
		})},
		// The category switcher stays on the item page (current one pre-selected),
		// so a wrong pick is corrected inline — no Back round-trip.
		h.categorySelectRow(ctx, serverID, cat, dest, targetID, category),
	}
	// The optional subcategory filter, whenever the category distinguishes more
	// than one. Subcategory ids are category-tree nodes, so they localize through
	// CategoryName like the top level.
	if len(subIDs) > 1 {
		type subRow struct{ id, name string }
		subs := make([]subRow, 0, len(subIDs))
		for _, id := range subIDs {
			name := cat.CategoryName(gamedata.GDID(id), lang)
			if name == "" {
				name = id
			}
			subs = append(subs, subRow{id: id, name: name})
		}
		sort.Slice(subs, func(a, b int) bool { return subs[a].name < subs[b].name })
		subOptions := make([]discordgo.SelectMenuOption, 0, len(subs)+1)
		subOptions = append(subOptions, discordgo.SelectMenuOption{
			Label:   h.loc.Render(ctx, serverID, "contracts.console.browse_sub_all", nil),
			Value:   subAll,
			Default: sub == "",
		})
		for _, s := range subs {
			subOptions = append(subOptions, discordgo.SelectMenuOption{
				Label:   truncate(s.name, 100),
				Value:   s.id,
				Default: s.id == sub,
			})
		}
		inner = append(inner, discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
			MenuType:    discordgo.StringSelectMenu,
			CustomID:    buildID(segBrowseSub, string(dest), targetID.String(), category),
			Placeholder: h.loc.Render(ctx, serverID, "contracts.console.browse_sub_placeholder", nil),
			Options:     subOptions,
		}}})
	}
	inner = append(inner, divider())
	if len(items) == 0 {
		inner = append(inner, discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.browse_empty", nil)})
	} else {
		options := make([]discordgo.SelectMenuOption, 0, end-start)
		for _, it := range items[start:end] {
			options = append(options, discordgo.SelectMenuOption{
				Label: truncate(it.name, 100),
				Value: it.id,
				Emoji: h.optionEmoji(gamedata.GDID(it.id)),
			})
		}
		inner = append(inner, discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
			MenuType:    discordgo.StringSelectMenu,
			CustomID:    buildID(segBrowseItems, string(dest), targetID.String(), category, intStr(page), sub),
			Placeholder: h.loc.Render(ctx, serverID, "contracts.console.pick_placeholder", nil),
			Options:     options,
		}}})
	}

	// With the inline category switcher above, Back exits the browser to the
	// destination view; Search stays for type-first users.
	nav := []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_search", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: buildID(segBrowseSearch, string(dest), targetID.String()),
		},
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: pickBackID(dest, targetID),
		},
	}
	if totalPages > 1 {
		nav = append(nav,
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.prev", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(segBrowseItems, string(dest), targetID.String(), category, intStr(page-1), sub),
				Disabled: page <= 0,
			},
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.next", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(segBrowseItems, string(dest), targetID.String(), category, intStr(page+1), sub),
				Disabled: page >= totalPages-1,
			})
	}
	inner = append(inner, divider(), discordgo.ActionsRow{Components: nav})
	return h.respondView(i, r, []discordgo.MessageComponent{discordgo.Container{Components: inner}}, true)
}

// handleBrowse serves the category view: a select CHOICE (value present) drills
// into that category's items; a plain click (Back buttons) renders the list.
func (h *Feature) handleBrowse(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := browseArgs(parts)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	if values := i.MessageComponentData().Values; len(values) == 1 {
		return h.renderBrowseItems(ctx, r, i, serverID, dest, targetID, values[0], 0, "")
	}
	return h.renderBrowseCategories(ctx, r, i, serverID, dest, targetID)
}

// handleBrowseItems serves the item view: a select CHOICE opens the quantity
// modal for the picked item; a pager click re-renders the page.
func (h *Feature) handleBrowseItems(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := browseArgs(parts)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	if len(parts) < 3 {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	category := parts[2]
	sub := ""
	if len(parts) > 4 {
		sub = parts[4]
	}
	if values := i.MessageComponentData().Values; len(values) == 1 {
		return h.openPickQtyModal(ctx, r, i, serverID, dest, targetID, values[0])
	}
	return h.renderBrowseItems(ctx, r, i, serverID, dest, targetID, category, argInt(parts, 3), sub)
}

// handleBrowseSub applies the subcategory filter chosen on the item page and
// re-renders it at page 0 (the "all" sentinel clears the filter).
func (h *Feature) handleBrowseSub(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := browseArgs(parts)
	if !ok || len(parts) < 3 {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	category := parts[2]
	sub := ""
	if values := i.MessageComponentData().Values; len(values) == 1 && values[0] != subAll {
		sub = values[0]
	}
	return h.renderBrowseItems(ctx, r, i, serverID, dest, targetID, category, 0, sub)
}

// handleBrowseSearch opens the destination's one-field query modal from the
// category page (the type-first alternative to browsing).
func (h *Feature) handleBrowseSearch(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := browseArgs(parts)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	seg := segMCAdd
	if dest == pickTemplateItem {
		seg = segMTAdd
	}
	comps := []discordgo.MessageComponent{
		h.searchInput(ctx, serverID, "contracts.console.search_hint", "", true),
	}
	return r.RespondModal(i.Interaction, buildID(seg, targetID.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_additem_title"), comps)
}

// browseArgs decodes the (dest, target) prefix every browse CustomID carries,
// accepting only the item destinations.
func browseArgs(parts []string) (pickDest, uuid.UUID, bool) {
	if len(parts) < 2 {
		return "", uuid.Nil, false
	}
	dest := pickDest(parts[0])
	if dest != pickContractItem && dest != pickTemplateItem {
		return "", uuid.Nil, false
	}
	targetID, ok := argUUID(parts, 1)
	return dest, targetID, ok
}

// --- delivery-location browser -----------------------------------------------

// renderLocationBrowser transforms the console message into the delivery-
// location picker: one select over every space object (they comfortably fit a
// single 25-option menu — six stations today), the current location
// pre-selected, plus Clear and Back. Modal-free: picking applies immediately
// (locations take no quantity).
func (h *Feature) renderLocationBrowser(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest pickDest, targetID uuid.UUID) error {
	cat := h.reg.Latest()
	if cat == nil {
		return h.consoleErr(ctx, r, i, serverID, fmt.Errorf("contracts: no gamedata versions loaded"))
	}
	lang := h.lang(ctx, serverID)

	current, err := h.currentLocation(ctx, serverID, dest, targetID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}

	type locRow struct{ id, name string }
	var locs []locRow
	for _, so := range cat.SpaceObjects() {
		name := cat.SpaceObjectName(so.ID, lang)
		if name == "" {
			name = string(so.ID)
		}
		locs = append(locs, locRow{id: string(so.ID), name: name})
	}
	sort.Slice(locs, func(a, b int) bool { return locs[a].name < locs[b].name })
	// A single select holds 25 options; the game ships six stations. Should the
	// catalog ever outgrow that, clamp loudly rather than render an invalid menu.
	if len(locs) > browsePageSize {
		h.log.Warn("contracts: space objects exceed one select; clamping", zap.Int("total", len(locs)))
		locs = locs[:browsePageSize]
	}

	options := make([]discordgo.SelectMenuOption, 0, len(locs))
	for _, l := range locs {
		options = append(options, discordgo.SelectMenuOption{
			Label:   truncate(l.name, 100),
			Value:   l.id,
			Default: l.id == current,
		})
	}

	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.browse_loc_title", nil)},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
			MenuType:    discordgo.StringSelectMenu,
			CustomID:    buildID(segLocBrowse, string(dest), targetID.String()),
			Placeholder: h.loc.Render(ctx, serverID, "contracts.console.pick_placeholder", nil),
			Options:     options,
		}}},
		divider(),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_loc_clear", nil),
				Style:    discordgo.DangerButton,
				CustomID: buildID(segLocClear, string(dest), targetID.String()),
				Disabled: current == "",
			},
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: pickBackID(dest, targetID),
			},
		}},
	}
	return h.respondView(i, r, []discordgo.MessageComponent{discordgo.Container{Components: inner}}, true)
}

// currentLocation loads the destination's stored delivery location gdid ("" =
// unset), for the pre-selection and the Clear button state.
func (h *Feature) currentLocation(ctx context.Context, serverID uuid.UUID, dest pickDest, targetID uuid.UUID) (string, error) {
	switch dest {
	case pickContractLoc:
		prog, err := h.repo.ProgressByIDScoped(ctx, serverID, targetID)
		if err != nil {
			return "", err
		}
		return prog.LocationGDID, nil
	case pickTemplateLoc:
		t, err := h.tpls.TemplateByID(ctx, serverID, targetID)
		if err != nil {
			return "", err
		}
		return t.LocationGDID, nil
	default:
		return "", ErrNotFound
	}
}

// handleLocBrowse serves the location picker: a select CHOICE applies the
// picked space object (re-authorized per destination); a plain click renders
// the picker.
func (h *Feature) handleLocBrowse(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := locArgs(parts)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	values := i.MessageComponentData().Values
	if len(values) != 1 {
		return h.renderLocationBrowser(ctx, r, i, serverID, dest, targetID)
	}
	if proceed, err := h.authorizePick(ctx, r, i, serverID, dest, targetID); !proceed {
		return err
	}
	return h.applyPick(ctx, r, i, serverID, dest, targetID, values[0], 0, true)
}

// handleLocClear clears the destination's delivery location.
func (h *Feature) handleLocClear(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := locArgs(parts)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	if proceed, err := h.authorizePick(ctx, r, i, serverID, dest, targetID); !proceed {
		return err
	}
	switch dest {
	case pickContractLoc:
		if err := h.repo.SetDeliveryLocation(ctx, serverID, targetID, "", "", invokerID(i)); err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
		return h.renderContractView(ctx, r, i, serverID, targetID, 0, true)
	default: // pickTemplateLoc, validated by locArgs
		if err := h.tpls.SetTemplateLocation(ctx, serverID, targetID, "", "", invokerID(i)); err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
		return h.renderTemplateEditView(ctx, r, i, serverID, targetID, 0, true)
	}
}

// locArgs decodes the (dest, target) prefix of a location-picker CustomID,
// accepting only the location destinations.
func locArgs(parts []string) (pickDest, uuid.UUID, bool) {
	if len(parts) < 2 {
		return "", uuid.Nil, false
	}
	dest := pickDest(parts[0])
	if dest != pickContractLoc && dest != pickTemplateLoc {
		return "", uuid.Nil, false
	}
	targetID, ok := argUUID(parts, 1)
	return dest, targetID, ok
}

// authorizePick re-checks the manager key for a browse/location apply (these
// segments aren't in gatedSegments — they carry a destination in the CustomID).
// proceed=false means it already responded; err carries only unexpected failures.
func (h *Feature) authorizePick(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, _ pickDest, _ uuid.UUID) (bool, error) {
	allowed, err := h.authorizedKey(ctx, i, serverID, keyManage)
	if err != nil {
		return false, fmt.Errorf("contracts: authorize %s: %w", keyManage, err)
	}
	if !allowed {
		return false, h.reply(ctx, r, i, serverID, "contracts.console.denied", nil)
	}
	return true, nil
}

// openPickQtyModal prompts for the quantity of a just-picked item — the shared
// last step of both the browse and search flows. The picked gdid rides the
// modal's CustomID.
func (h *Feature) openPickQtyModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest pickDest, targetID uuid.UUID, gdid string) error {
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_qty", inQty, discordgo.TextInputShort, "", true, 12),
	}
	return r.RespondModal(i.Interaction, buildID(segMBrowseQty, string(dest), targetID.String(), gdid),
		h.modalTitle(ctx, serverID, "contracts.console.modal_qty_title"), comps)
}

// submitBrowseQty applies a picked item with its quantity. This segment isn't in
// gatedSegments (it carries a destination in the CustomID), so it re-checks the
// manager key here — this is where the browse/search flows finally mutate.
func (h *Feature) submitBrowseQty(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	if len(parts) < 3 {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	dest := pickDest(parts[0])
	targetID, ok := argUUID(parts, 1)
	if !ok || (dest != pickContractItem && dest != pickTemplateItem) {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	gdid := parts[2]

	allowed, err := h.authorizedKey(ctx, i, serverID, keyManage)
	if err != nil {
		return fmt.Errorf("contracts: authorize %s: %w", keyManage, err)
	}
	if !allowed {
		return h.reply(ctx, r, i, serverID, "contracts.console.denied", nil)
	}

	qty, qerr := parseQty(modalTextValue(i.ModalSubmitData(), inQty))
	if qerr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_qty", nil))
	}
	return h.applyPick(ctx, r, i, serverID, dest, targetID, gdid, qty, true)
}
