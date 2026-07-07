package contracts

import (
	"context"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/numfmt"
)

// renderTemplateEditView renders (or updates) one template's edit page: a header
// (title + description), the default-value facts (rewards, deadline duration,
// delivery location), one Section per required item (icon + localized name +
// quantity, with an Edit accessory opening the qty modal), a remove-item select
// over the current page's items, and the control rows. Every mutation behind the
// buttons is gated on keyManage (gateMutation); the page itself is navigation.
func (h *Feature) renderTemplateEditView(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, tid uuid.UUID, page int, update bool) error {
	t, err := h.tpls.TemplateByID(ctx, serverID, tid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}

	header := "## " + truncate(t.Title, 200)
	if t.Description != "" {
		header += "\n\n" + t.Description
	}
	header += "\n\n" + h.templateFacts(ctx, serverID, t)
	inner := []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(header, 4000)}}

	totalPages := pageCount(len(t.Items))
	if page >= totalPages {
		page = totalPages - 1
	}
	if page < 0 {
		page = 0
	}
	if len(t.Items) == 0 {
		inner = append(inner, divider(), discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.tpl_no_items", nil)})
	} else {
		start := page * consolePageSize
		end := start + consolePageSize
		if end > len(t.Items) {
			end = len(t.Items)
		}
		pageItems := t.Items[start:end]
		for _, it := range pageItems {
			inner = append(inner, divider(), h.templateItemSection(ctx, serverID, it))
		}
		inner = append(inner, divider(), h.templateItemRemoveRow(ctx, serverID, pageItems))
	}

	if totalPages > 1 {
		inner = append(inner, divider(), h.templateItemPagerRow(ctx, serverID, tid, page, totalPages))
	}
	inner = append(inner, divider())
	inner = append(inner, h.templateControlRows(ctx, serverID, tid)...)
	components := []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	return h.respondView(i, r, components, update)
}

// templateFacts renders the template's default-value lines: rewards (only the
// non-zero ones), the deadline duration, and the delivery location.
func (h *Feature) templateFacts(ctx context.Context, serverID uuid.UUID, t Template) string {
	// Each reward on its own line, prefixed with its in-game icon, under a header
	// (mirrors the contract post's contractFacts). A template has no frozen payout
	// precision, so credits format at the current config.
	dec := h.cfg.PayoutDecimals
	var rewards []string
	if t.RewardCredits.IsPositive() {
		rewards = append(rewards, h.loc.Render(ctx, serverID, "contracts.embed.reward_credits", map[string]any{
			"Icon":   iconPrefix(h.emojiToken(emojiCorpoCredits)),
			"Amount": numfmt.Grouped(t.RewardCredits, dec),
		}))
	}
	if t.RewardReputation > 0 {
		rewards = append(rewards, h.loc.Render(ctx, serverID, "contracts.embed.reward_reputation", map[string]any{
			"Icon":   iconPrefix(h.emojiToken(emojiCorpoReputation)),
			"Amount": groupedInt(t.RewardReputation),
		}))
	}
	if t.RewardLicencePoints > 0 {
		rewards = append(rewards, h.loc.Render(ctx, serverID, "contracts.embed.reward_licence", map[string]any{
			"Icon":   iconPrefix(h.emojiToken(emojiLicensePoints)),
			"Amount": groupedInt(t.RewardLicencePoints),
		}))
	}
	// The members' share is meaningful only when there are credits to split.
	if t.RewardCredits.IsPositive() && t.ParticipantRewardFactor.IsPositive() {
		share := participantPool(t.RewardCredits, t.ParticipantRewardFactor)
		rewards = append(rewards, h.loc.Render(ctx, serverID, "contracts.embed.reward_members", map[string]any{
			"Icon":   iconPrefix(h.emojiToken(emojiMemberCredits)),
			"Amount": numfmt.Grouped(share, dec),
			"Factor": t.ParticipantRewardFactor.String(),
		}))
	}
	lines := make([]string, 0, 3)
	if len(rewards) > 0 {
		lines = append(lines, h.loc.Render(ctx, serverID, "contracts.embed.rewards_header", nil))
		lines = append(lines, rewards...)
	}
	if t.DeadlineMinutes > 0 {
		lines = append(lines, h.loc.Render(ctx, serverID, "contracts.console.tpl_duration_line", map[string]any{
			"Duration": formatTimeLeft(time.Duration(t.DeadlineMinutes) * time.Minute),
		}))
	} else {
		lines = append(lines, h.loc.Render(ctx, serverID, "contracts.console.tpl_no_duration", nil))
	}
	if t.LocationGDID != "" {
		lines = append(lines, h.loc.Render(ctx, serverID, "contracts.embed.location_line", map[string]any{
			"Location": h.spaceObjectDisplay(ctx, serverID, t.LocationGDID, t.LocationGDVersion),
		}))
	} else {
		lines = append(lines, h.loc.Render(ctx, serverID, "contracts.console.tpl_location_unset", nil))
	}
	return strings.Join(lines, "\n")
}

// templateItemSection is one required-item row: icon + localized name + qty,
// with an Edit accessory opening the qty modal (the current qty rides the
// CustomID so the modal can prefill without a by-item read).
func (h *Feature) templateItemSection(ctx context.Context, serverID uuid.UUID, it TemplateItem) discordgo.Section {
	text := "**" + truncate(h.itemDisplay(ctx, serverID, it.GDID, it.GDVersion), 200) + "** × " + groupedInt(it.Qty)
	return discordgo.Section{
		Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(text, 4000)}},
		Accessory: discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_open", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: buildID(segTIEdit, it.ID.String(), intStr(it.Qty)),
		},
	}
}

// templateItemRemoveRow is a select over the current page's items (≤3 options);
// choosing one removes it directly — a template item has no reservations to
// lose, so no typed confirmation.
func (h *Feature) templateItemRemoveRow(ctx context.Context, serverID uuid.UUID, pageItems []TemplateItem) discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, 0, len(pageItems))
	for _, it := range pageItems {
		options = append(options, discordgo.SelectMenuOption{
			Label: truncate(h.plainItemName(ctx, serverID, it), 100),
			Value: it.ID.String(),
			Emoji: h.optionEmoji(gamedata.GDID(it.GDID)),
		})
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    buildID(segTIDel),
		Placeholder: h.loc.Render(ctx, serverID, "contracts.console.remove_item_placeholder", nil),
		Options:     options,
	}}}
}

// plainItemName is the item's plain localized name for select options (no
// emoji token — custom emojis don't render inside option labels as markdown).
func (h *Feature) plainItemName(ctx context.Context, serverID uuid.UUID, it TemplateItem) string {
	cat := h.catalogFor(it.GDVersion)
	if cat == nil {
		return it.GDID
	}
	if name := cat.Name(gamedata.GDID(it.GDID), h.lang(ctx, serverID)); name != "" {
		return name
	}
	return it.GDID
}

// templateItemPagerRow pages the item sections within the edit page.
func (h *Feature) templateItemPagerRow(ctx context.Context, serverID uuid.UUID, tid uuid.UUID, page, totalPages int) discordgo.MessageComponent {
	id := tid.String()
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.prev", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segTView, id, intStr(page-1)),
			Disabled: page <= 0,
		},
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.next", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segTView, id, intStr(page+1)),
			Disabled: page >= totalPages-1,
		},
	}}
}

// templateControlRows are the edit page's action rows: [Back][Delete] then
// [Details][Rewards][Location][Add item]. The whole page is keyManage
// territory, so no per-button visibility checks — gateMutation re-authorizes
// every action anyway.
func (h *Feature) templateControlRows(ctx context.Context, serverID uuid.UUID, tid uuid.UUID) []discordgo.MessageComponent {
	id := tid.String()
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segTList, "0", "")},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_tpl_delete", nil), Style: discordgo.DangerButton, CustomID: buildID(segTDel, id)},
		}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_tpl_details", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segTEdit, id)},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_rewards", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segTRew, id)},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_location", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segTLoc, id)},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_add_item", nil), Style: discordgo.SuccessButton, CustomID: buildID(segTAdd, id)},
		}},
	}
}

// handleOpenTemplate opens a template's edit page (tview:<tid>:<page>).
func (h *Feature) handleOpenTemplate(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	return h.renderTemplateEditView(ctx, r, i, serverID, tid, argInt(parts, 1), true)
}

// handleTemplateItemRemove removes the item chosen in the remove select and
// re-renders the edit page.
func (h *Feature) handleTemplateItemRemove(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, _ []string) error {
	values := i.MessageComponentData().Values
	if len(values) != 1 {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateItemNotFound)
	}
	itemID, err := uuid.Parse(values[0])
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateItemNotFound)
	}
	tid, err := h.tpls.RemoveTemplateItem(ctx, serverID, itemID, invokerID(i))
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderTemplateEditView(ctx, r, i, serverID, tid, 0, true)
}
