package gamepick

import (
	"context"
	"fmt"
	"sort"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
)

// The category browser: the zero-typing item picker behind "Add item". The
// message transforms into a category list; picking a category shows its items
// ordered by name (with icons, paged); picking an item prompts for the quantity
// in a one-field modal, and the submit applies. A Search button on the category
// page opens the query modal for type-first users. Only item destinations
// (Browse=true) browse; locations use the location picker (locbrowse.go).
//
// Both views are plain navigation (mutations are re-checked when the quantity
// submits — see HandleQtySubmit), and both selects post back to their own view's
// CustomID: an interaction WITH a value is a choice, one without (Back / pager
// buttons) is a render.

// browsePageSize is the item-list page size — Discord's select-option cap.
const browsePageSize = 25

// browseDest decodes the (destCode, target) prefix every browse CustomID
// carries, accepting only registered browse destinations.
func (p *Picker) browseDest(parts []string) (Destination, uuid.UUID, bool) {
	if len(parts) < 2 {
		return Destination{}, uuid.Nil, false
	}
	dest, ok := p.dest(parts[0])
	if !ok || !dest.Browse {
		return Destination{}, uuid.Nil, false
	}
	targetID, ok := argUUID(parts, 1)
	return dest, targetID, ok
}

// categorySelectRow builds the category dropdown shared by both browser views:
// distinct display categories with item counts, ordered by localized name, the
// current one (if any) pre-selected. Choosing a category always renders its
// first item page, so the row doubles as the switcher on the item page.
func (p *Picker) categorySelectRow(ctx context.Context, serverID uuid.UUID, cat *gamedata.Catalog, dest Destination, targetID uuid.UUID, selected string) discordgo.MessageComponent {
	lang := p.langOf(ctx, serverID)

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
			Label:       truncate(c.name, 100),
			Value:       c.id,
			Description: p.key(ctx, serverID, "browse_cat_count", map[string]any{"Items": counts[c.id]}),
			Default:     c.id == selected,
		})
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    p.buildID(segBrowse, dest.Code, targetID.String()),
		Placeholder: p.key(ctx, serverID, "browse_placeholder", nil),
		Options:     options,
	}}}
}

// RenderBrowse is the feature entry point into the category browser: it resolves
// the destination code (which must be a registered browse destination) and opens
// the category list. Feature "Add item" handlers delegate here.
func (p *Picker) RenderBrowse(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, destCode string, targetID uuid.UUID) error {
	dest, ok := p.dest(destCode)
	if !ok || !dest.Browse {
		return p.notFound(ctx, r, i, serverID)
	}
	return p.renderBrowseCategories(ctx, r, i, serverID, dest, targetID)
}

// renderBrowseCategories transforms the message into the category list for an
// item pick destination.
func (p *Picker) renderBrowseCategories(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest Destination, targetID uuid.UUID) error {
	cat := p.cfg.Reg.Latest()
	if cat == nil {
		return p.cfg.OnError(ctx, r, i, serverID, fmt.Errorf("gamepick: no gamedata versions loaded"))
	}
	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: p.key(ctx, serverID, "browse_title", nil)},
		p.categorySelectRow(ctx, serverID, cat, dest, targetID, ""),
		divider(),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    p.key(ctx, serverID, "btn_search", nil),
				Style:    discordgo.PrimaryButton,
				CustomID: p.buildID(segBrowseSearch, dest.Code, targetID.String()),
			},
			discordgo.Button{
				Label:    p.key(ctx, serverID, "btn_back", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: dest.BackID(targetID),
			},
		}},
	}
	return r.UpdateComponentsV2(i.Interaction, []discordgo.MessageComponent{discordgo.Container{Components: inner}})
}

// renderBrowseItems transforms the message into one page of a category's items,
// ordered by localized name. sub ("" = all) narrows the page to one subcategory;
// the optional subcategory dropdown appears whenever the category distinguishes
// more than one.
func (p *Picker) renderBrowseItems(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest Destination, targetID uuid.UUID, category string, page int, sub string) error {
	cat := p.cfg.Reg.Latest()
	if cat == nil {
		return p.cfg.OnError(ctx, r, i, serverID, fmt.Errorf("gamepick: no gamedata versions loaded"))
	}
	lang := p.langOf(ctx, serverID)

	type itemRow struct{ id, name string }
	var items []itemRow
	subSeen := map[string]bool{}
	var subIDs []string
	for _, it := range cat.Items() {
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
		discordgo.TextDisplay{Content: p.key(ctx, serverID, "browse_items_title", map[string]any{
			"Category": catName, "Page": page + 1, "Pages": totalPages,
		})},
		p.categorySelectRow(ctx, serverID, cat, dest, targetID, category),
	}
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
			Label:   p.key(ctx, serverID, "browse_sub_all", nil),
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
			CustomID:    p.buildID(segBrowseSub, dest.Code, targetID.String(), category),
			Placeholder: p.key(ctx, serverID, "browse_sub_placeholder", nil),
			Options:     subOptions,
		}}})
	}
	inner = append(inner, divider())
	if len(items) == 0 {
		inner = append(inner, discordgo.TextDisplay{Content: p.key(ctx, serverID, "browse_empty", nil)})
	} else {
		options := make([]discordgo.SelectMenuOption, 0, end-start)
		for _, it := range items[start:end] {
			options = append(options, discordgo.SelectMenuOption{
				Label: truncate(it.name, 100),
				Value: it.id,
				Emoji: p.OptionEmoji(gamedata.GDID(it.id)),
			})
		}
		inner = append(inner, discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
			MenuType:    discordgo.StringSelectMenu,
			CustomID:    p.buildID(segBrowseItems, dest.Code, targetID.String(), category, intStr(page), sub),
			Placeholder: p.key(ctx, serverID, "pick_placeholder", nil),
			Options:     options,
		}}})
	}

	nav := []discordgo.MessageComponent{
		discordgo.Button{
			Label:    p.key(ctx, serverID, "btn_search", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: p.buildID(segBrowseSearch, dest.Code, targetID.String()),
		},
		discordgo.Button{
			Label:    p.key(ctx, serverID, "btn_back", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: dest.BackID(targetID),
		},
	}
	if totalPages > 1 {
		nav = append(nav,
			discordgo.Button{
				Label:    p.key(ctx, serverID, "prev", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: p.buildID(segBrowseItems, dest.Code, targetID.String(), category, intStr(page-1), sub),
				Disabled: page <= 0,
			},
			discordgo.Button{
				Label:    p.key(ctx, serverID, "next", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: p.buildID(segBrowseItems, dest.Code, targetID.String(), category, intStr(page+1), sub),
				Disabled: page >= totalPages-1,
			})
	}
	inner = append(inner, divider(), discordgo.ActionsRow{Components: nav})
	return r.UpdateComponentsV2(i.Interaction, []discordgo.MessageComponent{discordgo.Container{Components: inner}})
}

// HandleBrowse serves the category view: a select CHOICE (value present) drills
// into that category's items; a plain click (Back buttons) renders the list.
func (p *Picker) HandleBrowse(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := p.browseDest(parts)
	if !ok {
		return p.notFound(ctx, r, i, serverID)
	}
	if values := i.MessageComponentData().Values; len(values) == 1 {
		return p.renderBrowseItems(ctx, r, i, serverID, dest, targetID, values[0], 0, "")
	}
	return p.renderBrowseCategories(ctx, r, i, serverID, dest, targetID)
}

// HandleBrowseItems serves the item view: a select CHOICE opens the quantity
// modal for the picked item; a pager click re-renders the page.
func (p *Picker) HandleBrowseItems(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := p.browseDest(parts)
	if !ok {
		return p.notFound(ctx, r, i, serverID)
	}
	if len(parts) < 3 {
		return p.notFound(ctx, r, i, serverID)
	}
	category := parts[2]
	sub := ""
	if len(parts) > 4 {
		sub = parts[4]
	}
	if values := i.MessageComponentData().Values; len(values) == 1 {
		return p.openQtyModal(ctx, r, i, serverID, dest, targetID, values[0])
	}
	return p.renderBrowseItems(ctx, r, i, serverID, dest, targetID, category, argInt(parts, 3), sub)
}

// HandleBrowseSub applies the subcategory filter chosen on the item page and
// re-renders it at page 0 (the "all" sentinel clears the filter).
func (p *Picker) HandleBrowseSub(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := p.browseDest(parts)
	if !ok || len(parts) < 3 {
		return p.notFound(ctx, r, i, serverID)
	}
	category := parts[2]
	sub := ""
	if values := i.MessageComponentData().Values; len(values) == 1 && values[0] != subAll {
		sub = values[0]
	}
	return p.renderBrowseItems(ctx, r, i, serverID, dest, targetID, category, 0, sub)
}

// HandleBrowseSearch opens the destination's one-field query modal from the
// category page (the type-first alternative to browsing).
func (p *Picker) HandleBrowseSearch(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := p.browseDest(parts)
	if !ok {
		return p.notFound(ctx, r, i, serverID)
	}
	return dest.OpenModal(ctx, r, i, serverID, targetID)
}
