package contracts

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// renderContractView renders (or updates) the Contract view as Components V2: a
// Container with the contract header and one Section per item (its progress plus
// an "Open" accessory that drills into the Item view), then the management rows
// (Edit / Deadline / Add item · Republish / Cancel / Back) and an item pager.
func (h *Feature) renderContractView(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, cid uuid.UUID, page int, update bool) error {
	prog, err := h.repo.ProgressByIDScoped(ctx, serverID, cid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}

	header := "## " + h.loc.Render(ctx, serverID, "contracts.embed.title", map[string]any{"Title": prog.Title}) +
		"\n" + h.statusLine(ctx, serverID, prog)
	if prog.Description != "" {
		header += "\n\n" + prog.Description
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
		inner = append(inner, divider(), discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.embed.no_items", nil)})
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

	components := []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	if totalPages > 1 {
		components = append(components, h.itemPagerRow(ctx, serverID, cid, page, totalPages))
	}
	components = append(components, h.contractControlRows(ctx, serverID, cid)...)
	return h.respondView(i, r, components, update)
}

// itemSection is one item row: its progress with an "Open" accessory drilling
// into the Item view.
func (h *Feature) itemSection(ctx context.Context, serverID uuid.UUID, it Item) discordgo.Section {
	text := "**" + truncate(it.Name, 200) + "**\n" + h.loc.Render(ctx, serverID, "contracts.console.item_progress", map[string]any{
		"Delivered": it.DeliveredQty,
		"Reserved":  it.OutstandingReserved(),
		"Required":  it.RequiredQty,
		"Remaining": it.Remaining(),
	})
	return discordgo.Section{
		Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(text, 4000)}},
		Accessory: discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_open", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: buildID(segIRow, it.ID.String()),
		},
	}
}

// contractControlRows are the two management rows for the Contract view.
func (h *Feature) contractControlRows(ctx context.Context, serverID uuid.UUID, cid uuid.UUID) []discordgo.MessageComponent {
	id := cid.String()
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_change_name", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segCName, id)},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_change_deadline", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segCDead, id)},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_add_item", nil), Style: discordgo.SuccessButton, CustomID: buildID(segCAdd, id)},
		}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_republish", nil), Style: discordgo.PrimaryButton, CustomID: buildID(segRepub, id)},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_cancel", nil), Style: discordgo.DangerButton, CustomID: buildID(segCancel, id)},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segCBack)},
		}},
	}
}

// itemPagerRow pages the item sections within the Contract view.
func (h *Feature) itemPagerRow(ctx context.Context, serverID uuid.UUID, cid uuid.UUID, page, totalPages int) discordgo.MessageComponent {
	id := cid.String()
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.prev", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segIPage, id, intStr(page-1)),
			Disabled: page <= 0,
		},
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.next", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segIPage, id, intStr(page+1)),
			Disabled: page >= totalPages-1,
		},
	}}
}

func (h *Feature) handleItemPage(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, argInt(parts, 1), true)
}

// handleRepublish enqueues the repair task and gives ephemeral feedback (the
// console message stays as-is; the post is reposted asynchronously).
func (h *Feature) handleRepublish(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	action, err := h.repo.Republish(ctx, serverID, cid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	key := "contracts.console.republish_refreshing"
	if action == RepublishCreating {
		key = "contracts.console.republish_creating"
	}
	return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, key, nil))
}
