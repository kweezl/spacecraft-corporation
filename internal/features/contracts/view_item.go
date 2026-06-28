package contracts

import (
	"context"
	"strconv"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// renderItemView renders (or updates) the Item view as Components V2: a Container
// with the item's progress and a numbered participant list for the page, then one
// [Release][Remove] action row per participant (the number ties a line to its
// buttons; the buttons are keyed by user id, so the action is unambiguous), a
// participant pager, and a control row (Rename / Remove item / Back).
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

	body := "## " + h.loc.Render(ctx, serverID, "contracts.console.item_title", map[string]any{"Name": item.Name}) +
		"\n" + h.loc.Render(ctx, serverID, "contracts.console.item_progress", map[string]any{
		"Delivered": item.DeliveredQty,
		"Reserved":  item.OutstandingReserved(),
		"Required":  item.RequiredQty,
		"Remaining": item.Remaining(),
	})
	if len(item.Participants) == 0 {
		body += "\n\n" + h.loc.Render(ctx, serverID, "contracts.console.no_participants", nil)
	} else {
		for idx, p := range pageParts {
			body += "\n" + h.loc.Render(ctx, serverID, "contracts.console.participant_line", map[string]any{
				"Index": start + idx + 1, "User": p.UserID, "Outstanding": p.Outstanding(), "Delivered": p.Delivered,
			})
		}
		if totalPages > 1 {
			body += "\n\n" + h.loc.Render(ctx, serverID, "contracts.console.item_footer", map[string]any{"Page": page + 1, "Pages": totalPages})
		}
	}

	components := []discordgo.MessageComponent{discordgo.Container{Components: []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: truncate(body, 4000)},
	}}}
	for idx, p := range pageParts {
		components = append(components, h.participantRow(ctx, serverID, item.ID, p, start+idx+1))
	}
	if totalPages > 1 {
		components = append(components, h.participantPagerRow(ctx, serverID, item.ID, page, totalPages))
	}
	components = append(components, h.itemControlRow(ctx, serverID, item.ID, prog.ID))
	return h.respondView(i, r, components, update)
}

// participantRow is one participant's [Release][Remove] row; Release appears only
// when they still owe delivery (reserved > delivered).
func (h *Feature) participantRow(ctx context.Context, serverID uuid.UUID, itemID uuid.UUID, p Participant, index int) discordgo.MessageComponent {
	var btns []discordgo.MessageComponent
	if p.Outstanding() > 0 {
		btns = append(btns, discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_release", map[string]any{"Index": index}),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segPRel, itemID.String(), p.UserID),
		})
	}
	btns = append(btns, discordgo.Button{
		Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_remove_participant", map[string]any{"Index": index}),
		Style:    discordgo.DangerButton,
		CustomID: buildID(segPRem, itemID.String(), p.UserID),
	})
	return discordgo.ActionsRow{Components: btns}
}

// itemControlRow is the item-view control row: rename / remove item / back.
func (h *Feature) itemControlRow(ctx context.Context, serverID uuid.UUID, itemID, cid uuid.UUID) discordgo.MessageComponent {
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_change_name", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segIName, itemID.String())},
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_remove_item", nil), Style: discordgo.DangerButton, CustomID: buildID(segIDel, itemID.String())},
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segIBack, cid.String())},
	}}
}

// participantPagerRow pages the participant rows within the Item view.
func (h *Feature) participantPagerRow(ctx context.Context, serverID uuid.UUID, itemID uuid.UUID, page, totalPages int) discordgo.MessageComponent {
	id := itemID.String()
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
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
		},
	}}
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
