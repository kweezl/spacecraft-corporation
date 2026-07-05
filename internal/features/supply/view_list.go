package supply

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// renderListView renders the invoker's own supply requests: a status-filter
// multi-select, one row per request (title + status, an Open button), a New
// button, and prev/next pagination. Strictly self-scoped — ListByOwner is keyed
// on the invoking user, so a member only ever sees their own requests.
func (h *Feature) renderListView(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, mask, page int, update bool) error {
	entries, total, err := h.repo.ListByOwner(ctx, serverID, invokerID(i), statusesFromMask(mask), consolePageSize, page*consolePageSize)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	totalPages := (total + consolePageSize - 1) / consolePageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page >= totalPages {
		// total shrank since the page token was minted (e.g. concurrent deletion):
		// re-fetch at the clamped page so entries match the "Page X of Y" header.
		page = totalPages - 1
		entries, total, err = h.repo.ListByOwner(ctx, serverID, invokerID(i), statusesFromMask(mask), consolePageSize, page*consolePageSize)
		if err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
	}

	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "supply.console.list_title", map[string]any{
			"Page": page + 1, "Pages": totalPages,
		})},
		h.filterRow(ctx, serverID, mask),
		divider(),
	}
	if len(entries) == 0 {
		inner = append(inner, discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "supply.console.list_empty", nil)})
	} else {
		for _, e := range entries {
			inner = append(inner, discordgo.Section{
				Components: []discordgo.MessageComponent{discordgo.TextDisplay{
					Content: h.loc.Render(ctx, serverID, "supply.console.list_row", map[string]any{
						"Title":  truncate(e.Title, 80),
						"Status": h.statusName(ctx, serverID, e.Status),
					}),
				}},
				Accessory: discordgo.Button{
					Label:    h.loc.Render(ctx, serverID, "supply.console.btn_open", nil),
					Style:    discordgo.PrimaryButton,
					CustomID: buildID(segView, e.ID.String()),
				},
			})
		}
	}

	nav := []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "supply.console.btn_new", nil),
			Style:    discordgo.SuccessButton,
			CustomID: buildID(segNew),
		},
	}
	if totalPages > 1 {
		nav = append(nav,
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "supply.console.prev", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(segList, intStr(mask), intStr(page-1)),
				Disabled: page <= 0,
			},
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "supply.console.next", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(segList, intStr(mask), intStr(page+1)),
				Disabled: page >= totalPages-1,
			})
	}
	inner = append(inner, divider(), discordgo.ActionsRow{Components: nav})
	return h.respondView(i, r, []discordgo.MessageComponent{discordgo.Container{Components: inner}}, update)
}

// filterRow builds the status-filter multi-select, the current mask pre-selected.
func (h *Feature) filterRow(ctx context.Context, serverID uuid.UUID, mask int) discordgo.MessageComponent {
	opts := make([]discordgo.SelectMenuOption, 0, len(statusBits))
	for _, b := range statusBits {
		opts = append(opts, discordgo.SelectMenuOption{
			Label:   h.statusName(ctx, serverID, b.status),
			Value:   b.value,
			Default: mask&b.bit != 0,
		})
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    buildID(segFilter),
		Placeholder: h.loc.Render(ctx, serverID, "supply.console.filter_placeholder", nil),
		Options:     opts,
		MinValues:   intPtr(0),
		MaxValues:   len(statusBits),
	}}}
}

// handleFilter applies a chosen status filter and re-renders the list at page 0.
func (h *Feature) handleFilter(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	mask := maskFromValues(i.MessageComponentData().Values)
	return h.renderListView(ctx, r, i, serverID, mask, 0, true)
}

// statusName renders a status' display label.
func (h *Feature) statusName(ctx context.Context, serverID uuid.UUID, s Status) string {
	switch s {
	case StatusOpen:
		return h.loc.Render(ctx, serverID, "supply.console.status_open", nil)
	case StatusCompleted:
		return h.loc.Render(ctx, serverID, "supply.console.status_completed", nil)
	case StatusCancelled:
		return h.loc.Render(ctx, serverID, "supply.console.status_cancelled", nil)
	default:
		return string(s)
	}
}
