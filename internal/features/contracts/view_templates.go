package contracts

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// The templates list, in two modes sharing one renderer: the MANAGE mode is the
// server's template library (Edit accessories + Create new, behind keyManage),
// the PICK mode is the "New from template" chooser (Use accessories, behind
// the manager key). Search-by-title is a button opening a one-field modal — a
// Components V2 message cannot hold a text input — and the query then rides the
// pager/clear CustomIDs (encQuery), keeping the console stateless.

// renderTemplatesView renders (or updates) the template list for a mode
// (tplModeManage / tplModePick), one page at a time, filtered by query.
func (h *Feature) renderTemplatesView(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, mode string, page int, query string, update bool) error {
	entries, total, err := h.tpls.ListTemplates(ctx, serverID, query, consolePageSize, page*consolePageSize)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	totalPages := pageCount(total)
	if page >= totalPages {
		page = totalPages - 1
	}

	titleKey := "contracts.console.tpl_pick_title"
	if mode == tplModeManage {
		titleKey = "contracts.console.tpl_list_title"
	}
	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: "## " + h.loc.Render(ctx, serverID, titleKey, nil)},
		h.tplControlsRow(ctx, serverID, i, mode, query),
	}
	if total == 0 {
		inner = append(inner, divider(), discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.tpl_list_empty", nil)})
	} else {
		for _, e := range entries {
			inner = append(inner, divider(), h.templateSection(ctx, serverID, mode, e))
		}
		inner = append(inner, divider(), discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.tpl_list_footer",
			map[string]any{"Page": page + 1, "Pages": totalPages, "Total": total})})
	}
	inner = append(inner, divider(), h.tplNavRow(ctx, serverID, mode, page, totalPages, query))

	components := []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	return h.respondView(i, r, components, update)
}

// tplListSeg is the list segment re-rendering a mode (pager, clear).
func tplListSeg(mode string) string {
	if mode == tplModeManage {
		return segTList
	}
	return segTPick
}

// tplControlsRow is the list's top row: [Search][Clear][Create new]. Clear only
// appears while a query filters the list; Create new only in manage mode for
// managers (the handlers re-check regardless).
func (h *Feature) tplControlsRow(ctx context.Context, serverID uuid.UUID, i *discordgo.InteractionCreate, mode, query string) discordgo.MessageComponent {
	btns := []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_search", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: buildID(segTSearch, mode),
		},
	}
	if query != "" {
		btns = append(btns, discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_search_clear", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(tplListSeg(mode), "0", ""),
		})
	}
	if mode == tplModeManage && h.may(ctx, i, serverID, keyManage) {
		btns = append(btns, discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_tpl_new", nil),
			Style:    discordgo.SuccessButton,
			CustomID: buildID(segTNew),
		})
	}
	return discordgo.ActionsRow{Components: btns}
}

// templateSection is one template row: title + item count, with the mode's
// accessory (Edit drills into the edit page, Use opens the instantiate confirm).
func (h *Feature) templateSection(ctx context.Context, serverID uuid.UUID, mode string, e TemplateListEntry) discordgo.Section {
	text := "**" + truncate(e.Title, 200) + "**\n" +
		h.loc.Render(ctx, serverID, "contracts.console.tpl_entry", map[string]any{"Items": groupedInt(e.ItemCount)})
	accessory := discordgo.Button{
		Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_tpl_use", nil),
		Style:    discordgo.SuccessButton,
		CustomID: buildID(segTUse, e.ID.String()),
	}
	if mode == tplModeManage {
		accessory = discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_tpl_edit", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: buildID(segTView, e.ID.String(), "0"),
		}
	}
	return discordgo.Section{
		Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(text, 4000)}},
		Accessory:  accessory,
	}
}

// tplNavRow is [Back][Prev][Next]; the pager carries the mode's list segment,
// the page, and the encoded query.
func (h *Feature) tplNavRow(ctx context.Context, serverID uuid.UUID, mode string, page, totalPages int, query string) discordgo.MessageComponent {
	btns := []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segHome),
		},
	}
	if totalPages > 1 {
		seg, q := tplListSeg(mode), encQuery(query)
		btns = append(btns,
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.prev", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(seg, intStr(page-1), q),
				Disabled: page <= 0,
			},
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.next", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(seg, intStr(page+1), q),
				Disabled: page >= totalPages-1,
			})
	}
	return discordgo.ActionsRow{Components: btns}
}
