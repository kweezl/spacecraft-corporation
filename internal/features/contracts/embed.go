package contracts

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/kweezl/spacecraft-corporation/internal/numfmt"
)

// groupedCredits renders a credits value with space-grouped thousands at the
// configured payout precision (CONTRACT_PAYOUT_DECIMALS), matching the payout
// report. groupedInt does the same for whole-number figures — reward points and
// item quantities (delivered/reserved/required, counts) — that carry no
// fractional part. Both are display only; never use them for a value that must
// round-trip (modal input defaults, CustomIDs, CSV cells stay plain).
func (h *Feature) groupedCredits(d decimal.Decimal) string {
	return numfmt.Grouped(d, h.cfg.PayoutDecimals)
}

func groupedInt(n int) string { return numfmt.Grouped(decimal.NewFromInt(int64(n)), 0) }

// postItemsMax bounds how many item blocks the forum-post card renders; the rest
// collapse into a localized "+N more". It keeps the message within Discord's
// Components V2 component cap (and matches the old embed's effective 25-field
// ceiling). Contracts cap their items at BASES-style limits well under this.
const postItemsMax = 25

// postComponents builds the contract's progress card — the Components V2 starter
// message of its forum thread, re-rendered after every change as a single
// Container: a header (title + status + description), one text block per item, a
// "last updated" line, and (for open contracts) the reserve/deliver/release
// action row, all sharing the card's background. Open contracts show their
// deadline as Discord timestamp markdown (absolute date + live relative
// countdown, kept current client-side); closed ones show their end state and drop
// the buttons.
func (h *Feature) postComponents(ctx context.Context, serverID uuid.UUID, p Progress, withButtons bool) []discordgo.MessageComponent {
	header := "## " + h.loc.Render(ctx, serverID, "contracts.embed.title", map[string]any{"Title": p.Title}) +
		"\n" + h.statusLine(ctx, serverID, p)
	if p.Description != "" {
		header += "\n\n" + p.Description
	}
	inner := []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(header, embedDescMax)}}

	// Rewards + delivery location, when the contract carries any (typically copied
	// from a template; also editable on custom contracts).
	if facts := h.contractFacts(ctx, serverID, p.Contract); facts != "" {
		inner = append(inner, divider(), discordgo.TextDisplay{Content: truncate(facts, embedDescMax)})
	}

	if len(p.Items) == 0 {
		inner = append(inner, divider(), discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.embed.no_items", nil)})
	} else {
		inner = append(inner, divider())
		shown, overflow := p.Items, 0
		if len(shown) > postItemsMax {
			overflow = len(shown) - postItemsMax
			shown = shown[:postItemsMax]
		}
		for _, it := range shown {
			// Gamedata items lead with the catalog emoji icon + live-localized name;
			// legacy free-text items render their stored name as-is.
			name := truncate(it.Name, embedTitleMax)
			if it.GDID != "" {
				name = truncate(h.itemDisplay(ctx, serverID, it.GDID, it.GDVersion), embedTitleMax)
			}
			block := "**" + name + "**\n" + h.itemFieldValue(ctx, serverID, it)
			inner = append(inner, discordgo.TextDisplay{Content: truncate(block, embedDescMax)})
		}
		if overflow > 0 {
			inner = append(inner, discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.embed.items_more", map[string]any{"Count": groupedInt(overflow)})})
		}
	}

	// "Last updated" as Discord timestamp markdown: <t:…:R> renders the relative
	// time in each viewer's own timezone and stays current client-side, replacing
	// the embed's native footer timestamp.
	footer := h.loc.Render(ctx, serverID, "contracts.embed.updated_footer", nil) + " " + fmt.Sprintf("<t:%d:R>", p.LastRefreshedAt.Unix())
	inner = append(inner, divider(), discordgo.TextDisplay{Content: footer})

	if withButtons {
		inner = append(inner, divider())
		inner = append(inner, h.panelComponents(ctx, serverID)...)
	}
	return []discordgo.MessageComponent{discordgo.Container{Components: inner}}
}

// Discord embed field limits.
const (
	embedTitleMax = 256
	embedDescMax  = 4096
	// embedFieldValueMax is Discord's per-field value cap; embedOverflowReserve is
	// the rune budget held back for the "+N more" notice so it always fits.
	embedFieldValueMax   = 1024
	embedOverflowReserve = 64
)

// itemFieldValue renders one item's field value: the aggregate progress line,
// then one contributor line per participant (ordered by user). The contributor
// list is clamped to Discord's 1024-rune field limit; any overflow collapses to a
// localized "+N more" notice.
func (h *Feature) itemFieldValue(ctx context.Context, serverID uuid.UUID, it Item) string {
	// Show the still-pending reserved amount (reserved minus delivered), not the
	// gross reservation; once everything reserved has been delivered that figure is
	// zero, so drop the reserved part of the line entirely.
	itemKey := "contracts.embed.item_line"
	if it.OutstandingReserved() <= 0 {
		itemKey = "contracts.embed.item_line_done"
	}
	value := h.loc.Render(ctx, serverID, itemKey, map[string]any{
		"Delivered": groupedInt(it.DeliveredQty),
		"Reserved":  groupedInt(it.OutstandingReserved()),
		"Required":  groupedInt(it.RequiredQty),
		"Remaining": groupedInt(it.Remaining()),
	})
	for i, part := range it.Participants {
		// Show what the member still owes (outstanding), not their gross
		// reservation; once they have delivered it all the reserved figure is moot,
		// so drop it and show the delivered total alone.
		key := "contracts.embed.participant_line"
		if part.Outstanding() <= 0 {
			key = "contracts.embed.participant_line_done"
		}
		line := "\n" + h.loc.Render(ctx, serverID, key, map[string]any{
			"User":        part.UserID,
			"Outstanding": groupedInt(part.Outstanding()),
			"Delivered":   groupedInt(part.Delivered),
		})
		if utf8.RuneCountInString(value)+utf8.RuneCountInString(line) > embedFieldValueMax-embedOverflowReserve {
			value += "\n" + h.loc.Render(ctx, serverID, "contracts.embed.participants_more",
				map[string]any{"Count": groupedInt(len(it.Participants) - i)})
			break
		}
		value += line
	}
	return truncate(value, embedFieldValueMax)
}

// contractFacts renders a contract's rewards + delivery-location block, one line
// per set fact, "" when none are set (both card and console skip the block).
func (h *Feature) contractFacts(ctx context.Context, serverID uuid.UUID, c Contract) string {
	var lines []string
	// Each reward renders on its own line, prefixed with its in-game icon (absent
	// emoji degrades to plain text), under a localized header.
	var rewards []string
	if creditsSet(c.RewardCredits) {
		rewards = append(rewards, h.loc.Render(ctx, serverID, "contracts.embed.reward_credits", map[string]any{
			"Icon":   iconPrefix(h.emojiToken(emojiCorpoCredits)),
			"Amount": h.groupedCredits(*c.RewardCredits),
		}))
	}
	if c.RewardReputation != nil && *c.RewardReputation > 0 {
		rewards = append(rewards, h.loc.Render(ctx, serverID, "contracts.embed.reward_reputation", map[string]any{
			"Icon":   iconPrefix(h.emojiToken(emojiCorpoReputation)),
			"Amount": groupedInt(*c.RewardReputation),
		}))
	}
	if c.RewardLicencePoints != nil && *c.RewardLicencePoints > 0 {
		rewards = append(rewards, h.loc.Render(ctx, serverID, "contracts.embed.reward_licence", map[string]any{
			"Icon":   iconPrefix(h.emojiToken(emojiLicensePoints)),
			"Amount": groupedInt(*c.RewardLicencePoints),
		}))
	}
	// The members' share is meaningful only when there are credits to split; show
	// the credits members receive (same formula as the payout pool) with the
	// personal-credits icon, plus the split percent.
	if creditsSet(c.RewardCredits) && c.ParticipantRewardFactor.IsPositive() {
		share := c.RewardCredits.Mul(c.ParticipantRewardFactor).Shift(-2)
		rewards = append(rewards, h.loc.Render(ctx, serverID, "contracts.embed.reward_members", map[string]any{
			"Icon":   iconPrefix(h.emojiToken(emojiMemberCredits)),
			"Amount": h.groupedCredits(share),
			"Factor": c.ParticipantRewardFactor.String(),
		}))
	}
	if len(rewards) > 0 {
		lines = append(lines, h.loc.Render(ctx, serverID, "contracts.embed.rewards_header", nil))
		lines = append(lines, rewards...)
	}
	if c.LocationGDID != "" {
		lines = append(lines, h.loc.Render(ctx, serverID, "contracts.embed.location_line", map[string]any{
			"Location": h.spaceObjectDisplay(ctx, serverID, c.LocationGDID, c.LocationGDVersion),
		}))
	}
	// An officer marked the participant payouts as handed out (completed
	// contracts only — the mark is guarded on status).
	if c.PayoutsPaidAt != nil {
		lines = append(lines, h.loc.Render(ctx, serverID, "contracts.payout.paid", map[string]any{
			"Mention": "<@" + c.PayoutsPaidByUserID + ">",
		}))
	}
	return strings.Join(lines, "\n")
}

// statusLine renders the one-line status: time-left for open contracts, the
// terminal state otherwise.
func (h *Feature) statusLine(ctx context.Context, serverID uuid.UUID, p Progress) string {
	switch p.Status {
	case StatusOpen:
		open := h.loc.Render(ctx, serverID, "contracts.embed.status_open", nil)
		// A deadline-less contract shows "no deadline" instead of a countdown.
		if p.Deadline == nil {
			return open + " · " + h.loc.Render(ctx, serverID, "contracts.embed.no_deadline", nil)
		}
		// The deadline as Discord timestamp markdown: <t:…:f> renders the absolute
		// date/time in each viewer's own timezone, <t:…:R> the live relative
		// countdown ("in 2 days" → "2 days ago") that advances client-side. Because
		// the client keeps both current, an open contract's embed never needs the
		// server to periodically re-render just to stop the deadline looking stale.
		ts := p.Deadline.Unix()
		expires := h.loc.Render(ctx, serverID, "contracts.embed.expires", map[string]any{
			"At":  fmt.Sprintf("<t:%d:f>", ts),
			"Rel": fmt.Sprintf("<t:%d:R>", ts),
		})
		return open + " · " + expires
	case StatusCompleted:
		return h.loc.Render(ctx, serverID, "contracts.embed.status_completed", nil)
	case StatusExpired:
		return h.loc.Render(ctx, serverID, "contracts.embed.status_expired", nil)
	case StatusCancelled:
		return h.loc.Render(ctx, serverID, "contracts.embed.status_cancelled", nil)
	default:
		return ""
	}
}

// truncate clips s to at most n runes (Discord counts characters, not bytes).
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
