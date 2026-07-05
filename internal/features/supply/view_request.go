package supply

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// pageCount is the number of item pages for n items.
func pageCount(n int) int {
	p := (n + consolePageSize - 1) / consolePageSize
	if p < 1 {
		return 1
	}
	return p
}

// renderRequestView renders (or updates) the request view as Components V2: a
// header (title + status + description), the destination block, one Section per
// item (progress + an Open accessory that drills into the item view), an item
// pager, and the management rows. Owner-scoped: a request that isn't the
// invoker's yields ErrNotFound. Closed requests are read-only (only Back).
func (h *Feature) renderRequestView(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, rid uuid.UUID, page int, update bool) error {
	prog, err := h.repo.ProgressByIDOwned(ctx, serverID, invokerID(i), rid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}

	header := "## " + h.loc.Render(ctx, serverID, "supply.embed.title", map[string]any{"Title": prog.Title}) +
		"\n" + h.statusLine(ctx, serverID, prog)
	if prog.Description != "" {
		header += "\n\n" + prog.Description
	}
	if dest := h.destinationBlock(ctx, serverID, prog.Request); dest != "" {
		header += "\n\n" + dest
	}
	inner := []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(header, 4000)}}

	totalPages := pageCount(len(prog.Items))
	if page >= totalPages {
		page = totalPages - 1
	}
	if page < 0 {
		page = 0
	}
	if len(prog.Items) == 0 {
		inner = append(inner, divider(), discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "supply.embed.no_items", nil)})
	} else {
		start := page * consolePageSize
		end := start + consolePageSize
		if end > len(prog.Items) {
			end = len(prog.Items)
		}
		for _, it := range prog.Items[start:end] {
			inner = append(inner, divider(), h.itemSection(ctx, serverID, it))
		}
	}
	if totalPages > 1 {
		inner = append(inner, divider(), h.itemPagerRow(ctx, serverID, rid, page, totalPages))
	}
	inner = append(inner, divider())
	inner = append(inner, h.requestControlRows(ctx, serverID, prog)...)
	return h.respondView(i, r, []discordgo.MessageComponent{discordgo.Container{Components: inner}}, update)
}

// itemSection is one item row: name + progress with an Open accessory that drills
// into the item view (edit qty / remove).
func (h *Feature) itemSection(ctx context.Context, serverID uuid.UUID, it Item) discordgo.Section {
	name := truncate(h.itemDisplay(ctx, serverID, it.GDID, it.GDVersion), 200)
	text := "**" + name + "**\n" + h.itemFieldValue(ctx, serverID, it)
	return discordgo.Section{
		Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(text, 4000)}},
		Accessory: discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "supply.console.btn_open", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: buildID(segIRow, it.ID.String()),
		},
	}
}

// requestControlRows are the request view's action rows. Open requests get the
// full management set; closed requests are read-only (only Back).
func (h *Feature) requestControlRows(ctx context.Context, serverID uuid.UUID, prog Progress) []discordgo.MessageComponent {
	id := prog.ID.String()
	back := discordgo.Button{Label: h.loc.Render(ctx, serverID, "supply.console.btn_back", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segList)}
	if prog.Status != StatusOpen {
		return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{back}}}
	}
	row1 := discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		back,
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "supply.console.btn_add_item", nil), Style: discordgo.SuccessButton, CustomID: buildID(segRAdd, id)},
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "supply.console.btn_edit", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segREdit, id)},
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "supply.console.btn_republish", nil), Style: discordgo.PrimaryButton, CustomID: buildID(segRepub, id)},
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "supply.console.btn_close", nil), Style: discordgo.DangerButton, CustomID: buildID(segRClose, id)},
	}}
	row2 := discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "supply.console.btn_location", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segRLoc, id)},
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "supply.console.btn_system", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segRSys, id)},
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "supply.console.btn_reference", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segRRef, id)},
	}}
	return []discordgo.MessageComponent{row1, row2}
}

// itemPagerRow pages the item sections within the request view.
func (h *Feature) itemPagerRow(ctx context.Context, serverID uuid.UUID, rid uuid.UUID, page, totalPages int) discordgo.MessageComponent {
	id := rid.String()
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "supply.console.prev", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segIPage, id, intStr(page-1)),
			Disabled: page <= 0,
		},
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "supply.console.next", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segIPage, id, intStr(page+1)),
			Disabled: page >= totalPages-1,
		},
	}}
}

func (h *Feature) handleOpenRequest(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return h.renderRequestView(ctx, r, i, serverID, rid, 0, true)
}

func (h *Feature) handleItemPage(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return h.renderRequestView(ctx, r, i, serverID, rid, argInt(parts, 1), true)
}
