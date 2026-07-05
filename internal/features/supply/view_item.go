package supply

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// handleOpenItem drills into a single item's view from the request view.
func (h *Feature) handleOpenItem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return h.renderItemView(ctx, r, i, serverID, itemID, true)
}

// renderItemView renders one item's read-only progress (with the per-member
// breakdown) plus Edit-qty / Remove controls (open requests only) and a Back
// button to the request view. Owner-scoped via ProgressByItemOwned.
func (h *Feature) renderItemView(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, itemID uuid.UUID, update bool) error {
	prog, err := h.repo.ProgressByItemOwned(ctx, serverID, invokerID(i), itemID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	var item *Item
	for idx := range prog.Items {
		if prog.Items[idx].ID == itemID {
			item = &prog.Items[idx]
			break
		}
	}
	if item == nil {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}

	name := truncate(h.itemDisplay(ctx, serverID, item.GDID, item.GDVersion), 200)
	body := "## " + name + "\n" + h.itemFieldValue(ctx, serverID, *item)
	inner := []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(body, 4000)}}

	back := discordgo.Button{Label: h.loc.Render(ctx, serverID, "supply.console.btn_back", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segView, prog.ID.String())}
	row := []discordgo.MessageComponent{back}
	if prog.Status == StatusOpen {
		row = append(row,
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "supply.console.btn_edit_qty", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segIEdit, itemID.String())},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "supply.console.btn_remove", nil), Style: discordgo.DangerButton, CustomID: buildID(segIDel, itemID.String())},
		)
	}
	inner = append(inner, divider(), discordgo.ActionsRow{Components: row})
	return h.respondView(i, r, []discordgo.MessageComponent{discordgo.Container{Components: inner}}, update)
}
