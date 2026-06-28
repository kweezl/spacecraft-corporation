package contracts

import (
	"context"
	"strconv"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// renderItemView renders (or updates) the Item view as Components V2: a Container
// headed by the contract name then the item name + progress and a numbered
// participant list for the page, then a [Back][Prev][Next] row, a custom-only
// [Edit][Remove item] row, and one [Edit] row per participant (the number ties a
// line to its button; it opens the participant-manage modal). Edit/remove need
// keyCustom; participant management needs keyManage; both also require the
// contract to be open. The handlers re-check regardless (gateMutation).
func (h *Feature) renderItemView(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, itemID uuid.UUID, page int, update bool) error {
	prog, err := h.repo.ProgressByItemScoped(ctx, serverID, itemID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	var item Item
	found := false
	for _, it := range prog.Items {
		if it.ID == itemID {
			item, found = it, true
			break
		}
	}
	if !found {
		// The item was removed out from under us — drop back to the contract view.
		return h.renderContractView(ctx, r, i, serverID, prog.ID, 0, update)
	}

	totalPages := pageCount(len(item.Participants))
	if page >= totalPages {
		page = totalPages - 1
	}
	if page < 0 {
		page = 0
	}
	start := page * consolePageSize
	end := start + consolePageSize
	if end > len(item.Participants) {
		end = len(item.Participants)
	}
	pageParts := item.Participants[start:end]

	header := "## " + h.loc.Render(ctx, serverID, "contracts.embed.title", map[string]any{"Title": prog.Title}) +
		"\n### " + h.loc.Render(ctx, serverID, "contracts.console.item_title", map[string]any{"Name": item.Name}) +
		"\n" + h.itemProgress(ctx, serverID, item)

	open := prog.Status == StatusOpen
	canEditItem := open && h.may(ctx, i, serverID, keyCustom)
	canManage := open && h.may(ctx, i, serverID, keyManage)

	inner := []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(header, 4000)}}
	switch {
	case len(item.Participants) == 0:
		inner = append(inner, discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.no_participants", nil)})
	case canManage:
		// Each participant is a Section so its Edit button sits on the same row as
		// its figures (rather than a detached button keyed only by a line number).
		for idx, p := range pageParts {
			inner = append(inner, divider(), h.participantSection(ctx, serverID, item.ID, p, start+idx+1))
		}
	default:
		// Read-only viewer: a plain numbered list, no per-row buttons.
		lines := ""
		for idx, p := range pageParts {
			lines += "\n" + h.participantLine(ctx, serverID, p, start+idx+1)
		}
		inner = append(inner, discordgo.TextDisplay{Content: truncate(lines, 4000)})
	}
	if totalPages > 1 {
		inner = append(inner, discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.item_footer", map[string]any{"Page": page + 1, "Pages": totalPages})})
	}

	inner = append(inner, divider(), h.itemNavRow(ctx, serverID, item.ID, prog.ID, page, totalPages))
	if canEditItem {
		inner = append(inner, h.itemEditRow(ctx, serverID, item.ID))
	}
	components := []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	return h.respondView(i, r, components, update)
}

// itemNavRow is the item view's first row: Back to the contract, then Prev/Next
// over the participant pages (shown only when there is more than one page).
func (h *Feature) itemNavRow(ctx context.Context, serverID uuid.UUID, itemID, cid uuid.UUID, page, totalPages int) discordgo.MessageComponent {
	btns := []discordgo.MessageComponent{
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segIBack, cid.String())},
	}
	if totalPages > 1 {
		id := itemID.String()
		btns = append(btns,
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.prev", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(segPPage, id, strconv.Itoa(page-1)),
				Disabled: page <= 0,
			},
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.next", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(segPPage, id, strconv.Itoa(page+1)),
				Disabled: page >= totalPages-1,
			})
	}
	return discordgo.ActionsRow{Components: btns}
}

// itemEditRow is the custom-only [Edit][Remove item] row.
func (h *Feature) itemEditRow(ctx context.Context, serverID uuid.UUID, itemID uuid.UUID) discordgo.MessageComponent {
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_change_name", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segIEdit, itemID.String())},
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_remove_item", nil), Style: discordgo.DangerButton, CustomID: buildID(segIDel, itemID.String())},
	}}
}

// participantLine renders one participant's figures (the numbered text shared by
// the manage Section and the read-only list). The reserved figure is what's still
// outstanding (reserved minus delivered); once they have delivered all they
// reserved it is dropped.
func (h *Feature) participantLine(ctx context.Context, serverID uuid.UUID, p Participant, index int) string {
	key := "contracts.console.participant_line"
	if p.Outstanding() <= 0 {
		key = "contracts.console.participant_line_done"
	}
	return h.loc.Render(ctx, serverID, key, map[string]any{
		"Index": index, "User": p.UserID, "Outstanding": p.Outstanding(), "Delivered": p.Delivered,
	})
}

// participantSection is one participant as a Section: their figures with an Edit
// accessory button on the same row, opening the participant-manage modal.
func (h *Feature) participantSection(ctx context.Context, serverID uuid.UUID, itemID uuid.UUID, p Participant, index int) discordgo.Section {
	return discordgo.Section{
		Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(h.participantLine(ctx, serverID, p, index), 4000)}},
		Accessory: discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_participant_edit", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segPEdit, itemID.String(), p.UserID),
		},
	}
}

func (h *Feature) handleOpenItem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	return h.renderItemView(ctx, r, i, serverID, itemID, 0, true)
}

func (h *Feature) handleParticipantPage(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	return h.renderItemView(ctx, r, i, serverID, itemID, argInt(parts, 1), true)
}
