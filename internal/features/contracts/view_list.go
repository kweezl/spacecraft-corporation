package contracts

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// renderListView renders (or updates) the console list as a Components V2
// message: a Container in which each contract is a Section (its details plus an
// "Open" accessory button on the same row), followed by a status-filter
// multi-select and a [Prev][Next][Create] row. Cancel lives inside the opened
// contract view, not here.
func (h *Feature) renderListView(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, mask, page int, update bool) error {
	entries, total, err := h.repo.List(ctx, serverID, statusesFromMask(mask), consolePageSize, page*consolePageSize)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	totalPages := pageCount(total)
	if page >= totalPages {
		page = totalPages - 1
	}

	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: "## " + h.loc.Render(ctx, serverID, "contracts.console.list_title", nil)},
	}
	if total == 0 {
		inner = append(inner, discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.list_empty", nil)})
	} else {
		for _, e := range entries {
			inner = append(inner, divider(), h.contractSection(ctx, serverID, e))
		}
		inner = append(inner, divider(), discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.list_footer",
			map[string]any{"Page": page + 1, "Pages": totalPages, "Total": total})})
	}

	components := []discordgo.MessageComponent{
		discordgo.Container{Components: inner},
		h.filterRow(ctx, serverID, mask),
		h.listNavRow(ctx, serverID, mask, page, totalPages),
	}
	return h.respondView(i, r, components, update)
}

// contractSection is one contract row: its details with an "Open" accessory
// button that drills into the Contract view.
func (h *Feature) contractSection(ctx context.Context, serverID uuid.UUID, e ListEntry) discordgo.Section {
	text := "**" + truncate(e.Title, 200) + "**\n" + h.listEntryValue(ctx, serverID, e)
	if e.ThreadID == "" {
		text += " · " + h.loc.Render(ctx, serverID, "contracts.console.unpublished", nil)
	}
	return discordgo.Section{
		Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(text, 4000)}},
		Accessory: discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_open", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: buildID(segView, e.ID.String()),
		},
	}
}

// listEntryValue renders one contract row's detail: status/expiry, participant
// count, item count, and the reserved/delivered/required roll-up.
func (h *Feature) listEntryValue(ctx context.Context, serverID uuid.UUID, e ListEntry) string {
	return h.loc.Render(ctx, serverID, "contracts.console.list_entry", map[string]any{
		"Status":       h.statusLine(ctx, serverID, Progress{Contract: e.Contract}),
		"Participants": e.ParticipantCount,
		"Items":        e.ItemCount,
		"Reserved":     e.TotalReserved,
		"Delivered":    e.TotalDelivered,
		"Required":     e.TotalRequired,
	})
}

// filterRow is the status multi-select, prefilled with the active mask.
func (h *Feature) filterRow(ctx context.Context, serverID uuid.UUID, mask int) discordgo.MessageComponent {
	opts := make([]discordgo.SelectMenuOption, 0, len(statusBits))
	for _, b := range statusBits {
		opts = append(opts, discordgo.SelectMenuOption{
			Label:   h.loc.Render(ctx, serverID, "contracts.console.filter_"+b.value, nil),
			Value:   b.value,
			Default: mask&b.bit != 0,
		})
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    buildID(segFilter),
		Placeholder: h.loc.Render(ctx, serverID, "contracts.console.filter_placeholder", nil),
		MinValues:   intPtr(1),
		MaxValues:   len(statusBits),
		Options:     opts,
	}}}
}

// listNavRow is the prev/next (only when paged) + create row.
func (h *Feature) listNavRow(ctx context.Context, serverID uuid.UUID, mask, page, totalPages int) discordgo.MessageComponent {
	var btns []discordgo.MessageComponent
	if totalPages > 1 {
		btns = append(btns,
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.prev", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(segListPage, intStr(mask), intStr(page-1)),
				Disabled: page <= 0,
			},
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.next", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(segListPage, intStr(mask), intStr(page+1)),
				Disabled: page >= totalPages-1,
			})
	}
	btns = append(btns, discordgo.Button{
		Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_create", nil),
		Style:    discordgo.SuccessButton,
		CustomID: buildID(segCreate),
	})
	return discordgo.ActionsRow{Components: btns}
}

func (h *Feature) handleFilter(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	return h.renderListView(ctx, r, i, serverID, maskFromValues(i.MessageComponentData().Values), 0, true)
}

func (h *Feature) handleListPage(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	return h.renderListView(ctx, r, i, serverID, argInt(parts, 0), argInt(parts, 1), true)
}

func (h *Feature) handleOpenContract(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
}

// pageCount is the number of pages for a total at consolePageSize (at least 1).
func pageCount(total int) int {
	if total <= 0 {
		return 1
	}
	return (total + consolePageSize - 1) / consolePageSize
}
