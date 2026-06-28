package contracts

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
)

// renderEmbed builds the contract's progress embed — the starter message of its
// forum thread, re-rendered after every change. Open contracts show their
// deadline as Discord timestamp markdown (absolute date + live relative
// countdown, kept current client-side); closed ones show their end state.
func (h *Feature) renderEmbed(ctx context.Context, serverID uuid.UUID, p Progress) *discordgo.MessageEmbed {
	desc := ""
	if p.Description != "" {
		desc = p.Description + "\n\n"
	}
	desc += h.statusLine(ctx, serverID, p)

	fields := make([]*discordgo.MessageEmbedField, 0, len(p.Items))
	for _, it := range p.Items {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   truncate(it.Name, 256),
			Value:  h.itemFieldValue(ctx, serverID, it),
			Inline: false,
		})
	}
	if len(fields) == 0 {
		desc += "\n\n" + h.loc.Render(ctx, serverID, "contracts.embed.no_items", nil)
	}

	// Defensive clamp to Discord's embed limits, so a long title/description can
	// never make the forum-post create/edit fail with an opaque REST error (input
	// is already capped via the option MaxLength; this guards every other path).
	return &discordgo.MessageEmbed{
		Title:       truncate(h.loc.Render(ctx, serverID, "contracts.embed.title", map[string]any{"Title": p.Title}), embedTitleMax),
		Description: truncate(desc, embedDescMax),
		Fields:      fields,
		// Native "last updated" stamp: Discord renders it in the footer, localized
		// to each viewer's own timezone. Sourced from last_refreshed_at, which every
		// mutation advances — so it reflects when the contract was last changed
		// (RFC3339 carries the configured-zone offset asLocal stamped on the value).
		Timestamp: p.LastRefreshedAt.Format(time.RFC3339),
		Footer:    &discordgo.MessageEmbedFooter{Text: h.loc.Render(ctx, serverID, "contracts.embed.updated_footer", nil)},
	}
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
		"Delivered": it.DeliveredQty,
		"Reserved":  it.OutstandingReserved(),
		"Required":  it.RequiredQty,
		"Remaining": it.Remaining(),
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
			"Outstanding": part.Outstanding(),
			"Delivered":   part.Delivered,
		})
		if utf8.RuneCountInString(value)+utf8.RuneCountInString(line) > embedFieldValueMax-embedOverflowReserve {
			value += "\n" + h.loc.Render(ctx, serverID, "contracts.embed.participants_more",
				map[string]any{"Count": len(it.Participants) - i})
			break
		}
		value += line
	}
	return truncate(value, embedFieldValueMax)
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
