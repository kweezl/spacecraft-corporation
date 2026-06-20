package contracts

import (
	"context"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
)

// renderEmbed builds the contract's progress embed — the starter message of its
// forum thread, re-rendered after every change. Open contracts show their
// remaining time; closed ones show their end state.
func (h *Feature) renderEmbed(ctx context.Context, serverID uuid.UUID, p Progress) *discordgo.MessageEmbed {
	desc := ""
	if p.Description != "" {
		desc = p.Description + "\n\n"
	}
	desc += h.statusLine(ctx, serverID, p)

	fields := make([]*discordgo.MessageEmbedField, 0, len(p.Items))
	for _, it := range p.Items {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: truncate(it.Name, 256),
			Value: h.loc.Render(ctx, serverID, "contracts.embed.item_line", map[string]any{
				"Delivered": it.DeliveredQty,
				"Reserved":  it.ReservedQty,
				"Required":  it.RequiredQty,
				"Remaining": it.Remaining(),
			}),
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
	}
}

// Discord embed field limits.
const (
	embedTitleMax = 256
	embedDescMax  = 4096
)

// statusLine renders the one-line status: time-left for open contracts, the
// terminal state otherwise.
func (h *Feature) statusLine(ctx context.Context, serverID uuid.UUID, p Progress) string {
	switch p.Status {
	case StatusOpen:
		left := formatTimeLeft(time.Until(p.Deadline))
		return h.loc.Render(ctx, serverID, "contracts.embed.status_open", nil) + " · " +
			h.loc.Render(ctx, serverID, "contracts.embed.time_left", map[string]any{"Left": left})
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
