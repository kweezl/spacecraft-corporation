package contracts

import (
	"context"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// The payout report is a plain (non-Components-V2) message posted to the server's
// reports channel: a compact summary, the CSV export attached, and an ActionsRow
// with View-contract + Mark-paid buttons. Plain wins over V2 here — mentions ping
// natively, the CSV attaches without the V2 attachment:// dance (V2 forbids
// Content), and the Mark-paid edit is a simple in-place NewMessageEdit. The
// message id is persisted (MarkPayoutPosted) so Reprint and Mark-paid edit this
// one message rather than posting duplicates.

// reportContent renders the report body: a compact header (title, status line,
// and a link back to the contract thread when it has one) followed by the shared
// payout body (participant lines / remainder / priceless note / paid line). The
// detail lives in the CSV, so contractFacts and per-item lines are skipped.
// Returns the participant mentions to ping.
func (h *Feature) reportContent(ctx context.Context, prog Progress, rows []Payout) (string, []string) {
	sid := prog.ServerID
	var b strings.Builder
	b.WriteString("## " + h.loc.Render(ctx, sid, "contracts.embed.title", map[string]any{"Title": prog.Title}))
	b.WriteString("\n" + h.statusLine(ctx, sid, prog))
	if prog.ThreadID != "" {
		b.WriteString("\n" + h.loc.Render(ctx, sid, "contracts.payout.report_thread", map[string]any{"Thread": "<#" + prog.ThreadID + ">"}))
	}
	body, mentions := h.payoutContent(ctx, prog, rows)
	b.WriteString("\n\n" + body)
	return truncate(b.String(), 2000), mentions
}

// reportComponents builds the report's single ActionsRow: View contract (opens
// the ephemeral console view, manager-gated) and — until the payouts are marked
// paid — Mark paid (records it + edits this report in place).
func (h *Feature) reportComponents(ctx context.Context, prog Progress) []discordgo.MessageComponent {
	sid := prog.ServerID
	cid := prog.ID.String()
	btns := []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, sid, "contracts.payout.btn_view", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segRepView, cid),
		},
	}
	if prog.PayoutsPaidAt == nil {
		btns = append(btns, discordgo.Button{
			Label:    h.loc.Render(ctx, sid, "contracts.console.btn_payout_paid", nil),
			Style:    discordgo.SuccessButton,
			CustomID: buildID(segRepPaid, cid),
		})
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: btns}}
}

// handleReportView opens the (ephemeral) console contract view from the report's
// View button. The blanket manager gate (gateMutation on segRepView) already
// authorized it, so this is a plain read that never touches the shared report.
func (h *Feature) handleReportView(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, false)
}

// handleReportPaid marks a completed contract's payouts as paid from the report's
// Mark-paid button: the SQL guard is the authority (one winner), the loser gets
// an "already paid" notice, and the winner gets an ephemeral confirmation plus a
// best-effort in-place edit of this report (paid line appears, Mark-paid drops).
func (h *Feature) handleReportPaid(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
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
	h.editReportAfterPaid(ctx, serverID, cid)
	return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.payout_paid_ok", nil))
}

// editReportAfterPaid re-renders the posted payout report in place after a
// winning Mark-paid (from either the report or the console): the paid line
// appears and the Mark-paid button drops. Best-effort — the DB fact is durable
// and a Reprint re-posts, so a failure (message deleted, gateway down) is only
// logged. A no-op when the report was never posted (no stored message id).
func (h *Feature) editReportAfterPaid(ctx context.Context, serverID, contractID uuid.UUID) {
	prog, err := h.repo.ProgressByIDScoped(ctx, serverID, contractID)
	if err != nil {
		h.log.Warn("contracts: reload for report edit failed", zap.String("contract_id", contractID.String()), zap.Error(err))
		return
	}
	if prog.PayoutReportMessageID == "" {
		return // never posted (e.g. no reports channel yet); nothing to edit
	}
	rows, err := h.repo.Payouts(ctx, contractID)
	if err != nil {
		h.log.Warn("contracts: load payouts for report edit failed", zap.String("contract_id", contractID.String()), zap.Error(err))
		return
	}
	content, _ := h.reportContent(ctx, prog, rows)
	files := h.payoutFiles(ctx, prog, rows)
	if err := h.gw.EditChannelMessage(prog.PayoutReportChannelID, prog.PayoutReportMessageID, content, files, h.reportComponents(ctx, prog)); err != nil {
		h.log.Warn("contracts: edit payout report failed", zap.String("contract_id", contractID.String()), zap.Error(err))
	}
}
