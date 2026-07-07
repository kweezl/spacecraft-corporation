package contracts

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// renderListView renders (or updates) the console list as a single Components V2
// Container (one consistent "card"): a title, the status-filter multi-select, then
// each contract as a Section (its details plus an "Open" accessory button on the
// same row), and a trailing [Back][Prev][Next] row — all inside the container so
// the controls share its background. Creation lives on the dashboard (the Back
// button), Cancel inside the opened contract view.
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
		h.filterRow(ctx, serverID, mask),
	}
	if total == 0 {
		inner = append(inner, divider(), discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.list_empty", nil)})
	} else {
		for _, e := range entries {
			inner = append(inner, divider(), h.contractSection(ctx, serverID, e, mask, page))
		}
		inner = append(inner, divider(), discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.list_footer",
			map[string]any{"Page": page + 1, "Pages": totalPages, "Total": total})})
	}
	inner = append(inner, divider(), h.listNavRow(ctx, serverID, mask, page, totalPages))

	components := []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	return h.respondView(i, r, components, update)
}

// contractSection is one contract row: its details with an "Open" accessory
// button that drills into the Contract view. The current list filter (mask +
// page) rides in the Open CustomID so the contract view's Back returns to the
// same filtered page.
func (h *Feature) contractSection(ctx context.Context, serverID uuid.UUID, e ListEntry, mask, page int) discordgo.Section {
	text := "**" + truncate(e.Title, 200) + "**\n" + h.listEntryValue(ctx, serverID, e)
	if e.ThreadID == "" {
		text += " · " + h.loc.Render(ctx, serverID, "contracts.console.unpublished", nil)
	}
	return discordgo.Section{
		Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(text, 4000)}},
		Accessory: discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_open", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: buildID(segView, e.ID.String(), intStr(mask), intStr(page)),
		},
	}
}

// listEntryValue renders one contract row's detail: status/expiry, participant
// count, item count, and the reserved/delivered/required roll-up.
func (h *Feature) listEntryValue(ctx context.Context, serverID uuid.UUID, e ListEntry) string {
	return h.loc.Render(ctx, serverID, "contracts.console.list_entry", map[string]any{
		"Status":       h.statusLine(ctx, serverID, Progress{Contract: e.Contract}),
		"Participants": groupedInt(e.ParticipantCount),
		"Items":        groupedInt(e.ItemCount),
		"Reserved":     groupedInt(e.TotalReserved),
		"Delivered":    groupedInt(e.TotalDelivered),
		"Required":     groupedInt(e.TotalRequired),
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

// listNavRow is the back-to-dashboard button followed by prev/next (only when
// paged).
func (h *Feature) listNavRow(ctx context.Context, serverID uuid.UUID, mask, page, totalPages int) discordgo.MessageComponent {
	btns := []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segHome),
		},
	}
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
	return discordgo.ActionsRow{Components: btns}
}

func (h *Feature) handleFilter(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	return h.renderListView(ctx, r, i, serverID, maskFromValues(i.MessageComponentData().Values), 0, true)
}

func (h *Feature) handleListPage(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	return h.renderListView(ctx, r, i, serverID, argInt(parts, 0), argInt(parts, 1), true)
}

// handleOpenContract opens a contract (from the list's Open button or the item
// view's Back). The optional trailing parts carry the list filter context to
// return to; absent (e.g. from item Back) they fall back to the default filter.
func (h *Feature) handleOpenContract(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	mask, page := listCtx(parts, 1)
	return h.renderContractViewFrom(ctx, r, i, serverID, cid, 0, true, mask, page)
}

// pageCount is the number of pages for a total at consolePageSize (at least 1).
func pageCount(total int) int {
	if total <= 0 {
		return 1
	}
	return (total + consolePageSize - 1) / consolePageSize
}
