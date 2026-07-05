package contracts

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/outbox"
)

// payoutMentionCap bounds how many participants the payout comment @-mentions
// (like notifyMentionCap); the CSV always carries everyone.
const payoutMentionCap = 50

// payoutCSVName is the attachment filename of the payout export.
const payoutCSVName = "payout.csv"

// taskPayout computes, persists, and posts a completed contract's participant
// rewards to the server's reports channel. Idempotency is layered: the payout
// rows are the compute latch (compute + insert happen once, in one transaction —
// a retry with drifted catalog prices can never change posted figures) and
// payout_posted_at is the Discord latch (a re-run that already posted stops
// there). A console Reprint re-enqueues with Repost=true, which skips the
// posted-at latch and — since the report's channel+message id are persisted —
// edits the already-posted report in place rather than posting a duplicate. The
// crash window between posting and latching means a rare duplicate post the first
// time, after which the stored id makes every Reprint an in-place edit.
func (h *Feature) taskPayout(ctx context.Context, t outbox.Task) error {
	p, err := decodePayload(t)
	if err != nil {
		return outbox.Permanent(err)
	}
	prog, err := h.repo.ProgressByID(ctx, p.ContractID)
	if errors.Is(err, ErrNotFound) {
		return outbox.Permanent(err)
	}
	if err != nil {
		return err
	}
	// Defensive: the enqueue rides the completing transaction, so anything else
	// here is a bug, not a race.
	if prog.Status != StatusCompleted {
		return outbox.Permanent(errors.New("contracts: payout task for a non-completed contract"))
	}
	if prog.PayoutPostedAt != nil && !p.Repost {
		return nil
	}

	rows, err := h.repo.Payouts(ctx, p.ContractID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		if !creditsSet(prog.RewardCredits) || !prog.ParticipantRewardFactor.IsPositive() {
			// Defensive mirror of the enqueue guard.
			return outbox.Permanent(errors.New("contracts: payout task without a reward to distribute"))
		}
		res := computePayout(*prog.RewardCredits, prog.ParticipantRewardFactor, h.payoutItems(prog))
		if len(res.Shares) == 0 {
			// Unreachable for a completed contract (every item was fully delivered
			// by someone); bail loudly rather than post an empty report.
			return outbox.Permanent(errors.New("contracts: completed contract has no deliverers"))
		}
		for i := range res.Shares {
			// Snapshot display names now, while the members are likely still around;
			// the raw id is the durable fallback.
			name, ok := h.gw.MemberDisplayName(prog.ServerDiscordID, res.Shares[i].UserID)
			if !ok {
				name = res.Shares[i].UserID
			}
			res.Shares[i].UserName = name
		}
		if err := h.repo.SavePayouts(ctx, p.ContractID, res.Shares); err != nil {
			return err
		}
		rows = res.Shares
	}

	ch, ok := h.reports.ContractsReportsChannelID(ctx, prog.ServerID)
	if !ok {
		// No destination configured. The payouts are persisted; once an admin sets
		// the reports channel in /settings, a Reprint delivers them. Not latched, so
		// a later Repost run still works.
		h.log.Warn("contracts: payout computed but no reports channel configured — set it in /settings, then Reprint the payout",
			zap.String("contract_id", p.ContractID.String()))
		return nil
	}

	content, mentions := h.reportContent(ctx, prog, rows)
	comps := h.reportComponents(ctx, prog)
	now := time.Now()

	// Edit the already-posted report in place when we know where it is (a Reprint
	// after the first post) — no duplicate. Fall through to a fresh post on the
	// first run or when that message has been deleted.
	if prog.PayoutReportMessageID != "" {
		// Refresh the CSV too, so a Reprint after a language change re-renders both
		// the message and the attachment in the current language.
		err := h.gw.EditChannelMessage(prog.PayoutReportChannelID, prog.PayoutReportMessageID, content, []*discordgo.File{h.payoutCSVFile(ctx, prog, rows)}, comps)
		if err == nil {
			return h.repo.MarkPayoutPosted(ctx, p.ContractID, prog.PayoutReportChannelID, prog.PayoutReportMessageID, now)
		}
		if !isDeletedPost(err) {
			return permanentIfDiscord(err)
		}
		h.log.Warn("contracts: payout report message gone — reposting",
			zap.String("contract_id", p.ContractID.String()))
	}

	msgID, err := h.gw.PostChannelMessage(ch, content, mentions, []*discordgo.File{h.payoutCSVFile(ctx, prog, rows)}, comps)
	if err != nil {
		if isDeletedPost(err) {
			// The configured channel is gone; set a new one and Reprint. Not latched.
			h.log.Warn("contracts: configured reports channel is gone — set a new one in /settings, then Reprint",
				zap.String("contract_id", p.ContractID.String()))
			return nil
		}
		return permanentIfDiscord(err)
	}
	return h.repo.MarkPayoutPosted(ctx, p.ContractID, ch, msgID, now)
}

// payoutItems maps the contract's items to the computation's shape, resolving
// each unit value from the gamedata catalog stamped on the item (falling back
// to the latest loaded catalog). Free-text items, unknown gdids, and unpriced
// catalog entries get value zero — computePayout lists them as priceless. The
// float→decimal conversion here is the single point where money math touches a
// float (the catalog's Price), per the app-wide rule.
func (h *Feature) payoutItems(prog Progress) []payoutItem {
	items := make([]payoutItem, 0, len(prog.Items))
	for _, it := range prog.Items {
		unit := decimal.Decimal{}
		if it.GDID != "" {
			if cat := h.catalogFor(it.GDVersion); cat != nil {
				if gd, ok := cat.Item(gamedata.GDID(it.GDID)); ok && gd.Price > 0 {
					unit = decimal.NewFromFloat(gd.Price)
				}
			}
		}
		items = append(items, payoutItem{
			Name:        it.Name,
			UnitValue:   unit,
			RequiredQty: it.RequiredQty,
			Delivered:   it.Participants,
		})
	}
	return items
}

// payoutFigures recovers the report's aggregates from the contract + persisted
// rows: pool = credits × factor / 100 (both frozen once the contract left
// 'open', so this is stable across retries), remainder = pool − Σ amounts.
// zeroValue mirrors computePayout's flag from the persisted rows: every share
// percent is zero only when no item carried a value (a tiny pool can truncate
// every AMOUNT to zero while the shares stay positive — that is a normal split,
// not the nothing-to-weigh-by case).
func payoutFigures(prog Progress, rows []Payout) (pool, remainder decimal.Decimal, zeroValue bool) {
	pool = decimal.Decimal{}
	if prog.RewardCredits != nil {
		pool = prog.RewardCredits.Mul(prog.ParticipantRewardFactor).Shift(-2)
	}
	distributed := decimal.Decimal{}
	zeroValue = pool.IsPositive()
	for _, r := range rows {
		distributed = distributed.Add(r.Amount)
		if r.SharePercent.IsPositive() {
			zeroValue = false
		}
	}
	return pool, pool.Sub(distributed), zeroValue
}

// payoutContent renders the payout comment: a header with the pool and factor,
// one line per participant (mentions capped at payoutMentionCap — beyond it
// lines still render, they just don't ping), the undistributed remainder, and a
// note listing priceless items (re-derived from the catalog at render time;
// only the note is derived — amounts always come from the persisted rows). The
// all-priceless case renders an explanatory message instead of zero lines. A
// reprint after "mark paid" appends who paid.
func (h *Feature) payoutContent(ctx context.Context, prog Progress, rows []Payout) (string, []string) {
	pool, remainder, zeroValue := payoutFigures(prog, rows)
	sid := prog.ServerID

	var b strings.Builder
	b.WriteString(h.loc.Render(ctx, sid, "contracts.payout.header", map[string]any{
		"Pool":   pool.StringFixed(2),
		"Factor": prog.ParticipantRewardFactor.String(),
	}))

	var mentions []string
	if zeroValue {
		b.WriteString("\n")
		b.WriteString(h.loc.Render(ctx, sid, "contracts.payout.zero_value", nil))
	} else {
		for _, r := range rows {
			if len(mentions) < payoutMentionCap {
				mentions = append(mentions, r.UserID)
			}
			b.WriteString("\n")
			b.WriteString(h.loc.Render(ctx, sid, "contracts.payout.line", map[string]any{
				"Mention": "<@" + r.UserID + ">",
				"Amount":  r.Amount.StringFixed(2),
			}))
		}
		if remainder.IsPositive() {
			b.WriteString("\n")
			b.WriteString(h.loc.Render(ctx, sid, "contracts.payout.remainder", map[string]any{
				"Amount": remainder.StringFixed(2),
			}))
		}
	}
	var priceless []string
	// payoutItems preserves prog.Items order/length, so zip by index to recover
	// each priceless item's localized name (payoutItem.Name is the raw snapshot).
	for idx, it := range h.payoutItems(prog) {
		if !it.UnitValue.IsPositive() {
			priceless = append(priceless, h.itemName(ctx, sid, prog.Items[idx]))
		}
	}
	if len(priceless) > 0 && !zeroValue {
		b.WriteString("\n")
		b.WriteString(h.loc.Render(ctx, sid, "contracts.payout.priceless", map[string]any{
			"Items": strings.Join(priceless, ", "),
		}))
	}
	if prog.PayoutsPaidAt != nil {
		b.WriteString("\n")
		b.WriteString(h.loc.Render(ctx, sid, "contracts.payout.paid", map[string]any{
			"Mention": "<@" + prog.PayoutsPaidByUserID + ">",
		}))
	}
	return truncate(b.String(), 2000), mentions
}

// payoutCSVFile wraps the CSV export as a Discord file attachment (a fresh
// single-use reader each call), rendered in the server's current language.
func (h *Feature) payoutCSVFile(ctx context.Context, prog Progress, rows []Payout) *discordgo.File {
	return &discordgo.File{
		Name:        payoutCSVName,
		ContentType: "text/csv; charset=utf-8",
		Reader:      bytes.NewReader(h.payoutCSV(ctx, prog, rows)),
	}
}

// payoutCSV builds the spreadsheet export: the contract id + title, participant
// name + id, the delivered quantity per contract item, the value share, and the
// credit reward. The contract id/title lead every row so exports from several
// contracts can be concatenated and still identify (and pivot/group by) their
// source. The buffer is seeded with a UTF-8 BOM so spreadsheet apps decode
// Cyrillic (and any other non-ASCII) names correctly on plain double-click import.
func (h *Feature) payoutCSV(ctx context.Context, prog Progress, rows []Payout) []byte {
	// item index → (user id → delivered qty), in the contract's item order.
	perItem := make([]map[string]int, len(prog.Items))
	for i, it := range prog.Items {
		perItem[i] = make(map[string]int, len(it.Participants))
		for _, p := range it.Participants {
			perItem[i][p.UserID] = p.Delivered
		}
	}

	var buf bytes.Buffer
	buf.WriteString("\uFEFF") // UTF-8 BOM

	sid := prog.ServerID
	contractID := prog.ID.String()
	header := []string{
		h.loc.Render(ctx, sid, "contracts.payout.csv_contract_id", nil),
		h.loc.Render(ctx, sid, "contracts.payout.csv_contract", nil),
		h.loc.Render(ctx, sid, "contracts.payout.csv_participant", nil),
		h.loc.Render(ctx, sid, "contracts.payout.csv_user_id", nil),
	}
	for _, it := range prog.Items {
		header = append(header, h.itemName(ctx, sid, it))
	}
	header = append(header,
		h.loc.Render(ctx, sid, "contracts.payout.csv_share", nil),
		h.loc.Render(ctx, sid, "contracts.payout.csv_amount", nil))
	writeCSVRow(&buf, header)

	for _, r := range rows {
		rec := []string{contractID, prog.Title, r.UserName, r.UserID}
		for i := range prog.Items {
			rec = append(rec, intStr(perItem[i][r.UserID]))
		}
		rec = append(rec, r.SharePercent.StringFixed(6), r.Amount.StringFixed(2))
		writeCSVRow(&buf, rec)
	}
	return buf.Bytes()
}

// writeCSVRow writes one CSV record with every field explicitly wrapped in double
// quotes (any embedded quote doubled) and terminated by CRLF, per RFC 4180 \u2014 so a
// comma, quote, or newline in a contract title, participant name, or item name
// can never break the row structure, and every value is unambiguously quoted.
func writeCSVRow(buf *bytes.Buffer, fields []string) {
	for i, f := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteByte('"')
		buf.WriteString(strings.ReplaceAll(f, `"`, `""`))
		buf.WriteByte('"')
	}
	buf.WriteString("\r\n")
}
