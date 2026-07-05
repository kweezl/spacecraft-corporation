package contracts

import (
	"context"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// renderContractView renders the Contract view with the default (active) list
// filter as the Back target — used by mutations and other non-list entry points,
// which have no list origin to remember.
func (h *Feature) renderContractView(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, cid uuid.UUID, page int, update bool) error {
	return h.renderContractViewFrom(ctx, r, i, serverID, cid, page, update, defaultMask, 0)
}

// renderContractViewFrom renders (or updates) the Contract view as Components V2:
// a Container with the contract header and one Section per item (its progress
// plus an "Open" accessory that drills into the Item view), then the management
// rows (Edit / Deadline / Add item · Republish / Cancel / Back) and an item
// pager. listMask/listPage are the list filter to return to — carried into the
// Back button and the item pager so drilling in and back preserves the filter.
func (h *Feature) renderContractViewFrom(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, cid uuid.UUID, page int, update bool, listMask, listPage int) error {
	prog, err := h.repo.ProgressByIDScoped(ctx, serverID, cid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}

	header := "## " + h.loc.Render(ctx, serverID, "contracts.embed.title", map[string]any{"Title": prog.Title}) +
		"\n" + h.statusLine(ctx, serverID, prog)
	if prog.Description != "" {
		header += "\n\n" + prog.Description
	}
	if facts := h.contractFacts(ctx, serverID, prog.Contract); facts != "" {
		header += "\n\n" + facts
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

	if totalPages > 1 {
		inner = append(inner, divider(), h.itemPagerRow(ctx, serverID, cid, page, totalPages, listMask, listPage))
	}
	inner = append(inner, divider())
	inner = append(inner, h.contractControlRows(ctx, serverID, i, prog, listMask, listPage)...)
	components := []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	return h.respondView(i, r, components, update)
}

// itemSummary is one item's display text: its name (with the catalog emoji icon
// for a gamedata-linked item) and progress line.
func (h *Feature) itemSummary(ctx context.Context, serverID uuid.UUID, it Item) string {
	name := truncate(it.Name, 200)
	if it.GDID != "" {
		name = truncate(h.itemDisplay(ctx, serverID, it.GDID, it.GDVersion), 200)
	}
	return "**" + name + "**\n" + h.itemProgress(ctx, serverID, it)
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
// [Back][Republish] and a second row of [Edit][Add item][Rewards][Location]
// [Cancel] (exactly Discord's 5-button row cap). A closed (terminal) contract is
// read-only — only Back is shown, except a COMPLETED contract with participant
// rewards, which gets a payout row: [Reprint payout] (re-post the report from
// the persisted rows) and [Mark paid] (once, until pressed). Every edit, Republish,
// and the payout actions need the single contract-manager key (keyManage). Rows
// that would be empty are dropped (Discord rejects an empty row), but Back keeps
// the first row non-empty.
func (h *Feature) contractControlRows(ctx context.Context, serverID uuid.UUID, i *discordgo.InteractionCreate, prog Progress, listMask, listPage int) []discordgo.MessageComponent {
	id := prog.ID.String()
	open := prog.Status == StatusOpen
	isManager := h.may(ctx, i, serverID, keyManage)
	canEdit := open && isManager

	nav := []discordgo.MessageComponent{
		discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_back", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segCBack, intStr(listMask), intStr(listPage))},
	}
	if open && isManager {
		nav = append(nav, discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_republish", nil), Style: discordgo.PrimaryButton, CustomID: buildID(segRepub, id)})
	}
	rows := []discordgo.MessageComponent{discordgo.ActionsRow{Components: nav}}

	var manage []discordgo.MessageComponent
	if canEdit {
		manage = append(manage,
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_change_name", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segCEdit, id)},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_add_item", nil), Style: discordgo.SuccessButton, CustomID: buildID(segCAdd, id)},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_rewards", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segCRew, id)},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_location", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segCLoc, id)},
			discordgo.Button{Label: h.loc.Render(ctx, serverID, "contracts.console.btn_cancel", nil), Style: discordgo.DangerButton, CustomID: buildID(segCancel, id)},
		)
	}
	// Post-completion payout controls, gated by the manager key like edits. Shown
	// only when the completion actually enqueued a payout (positive credits AND
	// factor — the same predicate as finishIfComplete).
	if prog.Status == StatusCompleted && isManager && creditsSet(prog.RewardCredits) && prog.ParticipantRewardFactor.IsPositive() {
		manage = append(manage, discordgo.Button{
			Label: h.loc.Render(ctx, serverID, "contracts.console.btn_payout_reprint", nil), Style: discordgo.SecondaryButton, CustomID: buildID(segPayRep, id),
		})
		if prog.PayoutsPaidAt == nil {
			manage = append(manage, discordgo.Button{
				Label: h.loc.Render(ctx, serverID, "contracts.console.btn_payout_paid", nil), Style: discordgo.SuccessButton, CustomID: buildID(segPayPaid, id),
			})
		}
	}
	if len(manage) > 0 {
		rows = append(rows, discordgo.ActionsRow{Components: manage})
	}
	return rows
}

// itemPagerRow pages the item sections within the Contract view, carrying the
// list-filter context so paginating items doesn't lose the Back target.
func (h *Feature) itemPagerRow(ctx context.Context, serverID uuid.UUID, cid uuid.UUID, page, totalPages, listMask, listPage int) discordgo.MessageComponent {
	id := cid.String()
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.prev", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segIPage, id, intStr(page-1), intStr(listMask), intStr(listPage)),
			Disabled: page <= 0,
		},
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.next", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segIPage, id, intStr(page+1), intStr(listMask), intStr(listPage)),
			Disabled: page >= totalPages-1,
		},
	}}
}

func (h *Feature) handleItemPage(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	mask, page := listCtx(parts, 2)
	return h.renderContractViewFrom(ctx, r, i, serverID, cid, argInt(parts, 1), true, mask, page)
}

// handlePayoutReprint re-enqueues the payout task with Repost set, so the
// worker re-posts the report from the persisted contract_payouts rows (or
// computes them first if the original task never got to — e.g. the contract
// completed before its forum post existed). Ephemeral feedback; the comment
// lands asynchronously like every Discord side effect.
func (h *Feature) handlePayoutReprint(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	// A Reprint has nowhere to post without a configured reports channel; decline
	// with an explanation rather than silently enqueuing a task that will skip.
	if _, ok := h.reports.ContractsReportsChannelID(ctx, serverID); !ok {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.payout_no_reports_channel", nil))
	}
	if err := h.repo.RequestPayoutRepost(ctx, serverID, cid); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.payout_reprint_queued", nil))
}

// handlePayoutPaid marks a completed contract's payouts as handed out in game.
// The repository guard is the authority (SQL-checked: completed + not yet
// paid), so a concurrent double-press has exactly one winner; the loser gets an
// "already paid" notice. The winner's view re-renders with the paid fact line
// in place of the button.
func (h *Feature) handlePayoutPaid(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	won, err := h.repo.MarkPayoutsPaid(ctx, serverID, cid, invokerID(i), time.Now())
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	if !won {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.payout_already_paid", nil))
	}
	// Reflect the paid state on the already-posted public report too (best-effort),
	// so its Mark-paid button drops in step with the console re-render.
	h.editReportAfterPaid(ctx, serverID, cid)
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
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
