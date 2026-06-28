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
			// Template contracts have fixed items: no drill-down (the item view is
			// all mutations), so render them as plain read-only text rather than a
			// Section with an "Open" accessory.
			if prog.Kind == KindCustom {
				inner = append(inner, divider(), h.itemSection(ctx, serverID, it))
			} else {
				inner = append(inner, divider(), discordgo.TextDisplay{Content: truncate(h.itemSummary(ctx, serverID, it), 4000)})
			}
		}
	}

	if totalPages > 1 {
		inner = append(inner, divider(), h.itemPagerRow(ctx, serverID, cid, page, totalPages))
	}
	inner = append(inner, divider())
	inner = append(inner, h.contractControlRows(ctx, serverID, i, cid, prog.Kind, prog.Status)...)
	components := []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	return h.respondView(i, r, components, update)
}

// itemSummary is one item's display text: its name and progress line.
func (h *Feature) itemSummary(ctx context.Context, serverID uuid.UUID, it Item) string {
	return "**" + truncate(it.Name, 200) + "**\n" + h.itemProgress(ctx, serverID, it)
}

// itemProgress renders an item's progress line: delivered/required plus the
// still-outstanding reserved (reserved minus delivered). Once everything reserved
// has been delivered that figure is zero, so the reserved part is dropped — there
// is nothing left in reserve.
func (h *Feature) itemProgress(ctx context.Context, serverID uuid.UUID, it Item) string {
	key := "contracts.console.item_progress"
	if it.OutstandingReserved() <= 0 {
		key = "contracts.console.item_progress_done"
	}
	return h.loc.Render(ctx, serverID, key, map[string]any{
		"Delivered": it.DeliveredQty,
		"Reserved":  it.OutstandingReserved(),
		"Required":  it.RequiredQty,
	})
}

// itemSection is one item row: its progress with an "Open" accessory drilling
// into the Item view (custom contracts only).
func (h *Feature) itemSection(ctx context.Context, serverID uuid.UUID, it Item) discordgo.Section {
	return discordgo.Section{
		Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(h.itemSummary(ctx, serverID, it), 4000)}},
		Accessory: discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_open", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: buildID(segIRow, it.ID.String()),
		},
	}
}

// contractControlRows are the Contract view's action rows: a first row of
// [Back][Republish] and a second row of [Edit][Add item][Cancel]. A closed
// (terminal) contract is read-only — only Back is shown. Edit and Cancel need the
// kind's key (custom edits name/description/deadline; template edits the deadline
// only); Add item is custom-only under keyCustom; Republish is independent
// (keyRepublish). Rows that would be empty are dropped (Discord rejects an empty
// row), but Back keeps the first row non-empty.
func (h *Feature) contractControlRows(ctx context.Context, serverID uuid.UUID, i *discordgo.InteractionCreate, cid uuid.UUID, kind Kind, status Status) []discordgo.MessageComponent {
	id := cid.String()
	open := status == StatusOpen
	canEdit := open && h.may(ctx, i, serverID, keyForKind(kind))

	nav := []discordgo.MessageComponent{
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segCBack)},
	}
	if open && h.may(ctx, i, serverID, keyRepublish) {
		nav = append(nav, discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_republish", nil), Style: discordgo.PrimaryButton, CustomID: buildID(segRepub, id)})
	}
	rows := []discordgo.MessageComponent{discordgo.ActionsRow{Components: nav}}

	var manage []discordgo.MessageComponent
	if canEdit {
		manage = append(manage, discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_change_name", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segCEdit, id)})
	}
	if open && kind == KindCustom && h.may(ctx, i, serverID, keyCustom) {
		manage = append(manage, discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_add_item", nil), Style: discordgo.SuccessButton, CustomID: buildID(segCAdd, id)})
	}
	if canEdit {
		manage = append(manage, discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_cancel", nil), Style: discordgo.DangerButton, CustomID: buildID(segCancel, id)})
	}
	if len(manage) > 0 {
		rows = append(rows, discordgo.ActionsRow{Components: manage})
	}
	return rows
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
	// Closed contracts are read-only — the button is hidden, but re-check in case
	// of a crafted id.
	prog, err := h.repo.ProgressByIDScoped(ctx, serverID, cid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	if prog.Status != StatusOpen {
		return h.consoleErr(ctx, r, i, serverID, ErrClosed)
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
