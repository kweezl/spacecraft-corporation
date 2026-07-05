package supply

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/roman"
)

// Discord component/field caps.
const (
	embedTitleMax        = 256
	embedDescMax         = 4096
	embedFieldValueMax   = 1024
	embedOverflowReserve = 64
	// postItemsMax bounds how many item blocks the card renders; the rest collapse
	// into a localized "+N more".
	postItemsMax = 25
)

// postComponents builds a request's progress card — the Components V2 starter
// message of its forum thread, re-rendered after every change as one Container:
// a header (title + owner + status + description), the optional destination
// block (location / system / planet / reference), one text block per item, a
// "last updated" line, and (while open) the reserve/deliver/release panel.
func (h *Feature) postComponents(ctx context.Context, serverID uuid.UUID, p Progress, withButtons bool) []discordgo.MessageComponent {
	header := "## " + h.loc.Render(ctx, serverID, "supply.embed.title", map[string]any{"Title": p.Title}) +
		"\n" + h.loc.Render(ctx, serverID, "supply.embed.owner", map[string]any{"User": p.OwnerUserID}) +
		"\n" + h.statusLine(ctx, serverID, p)
	if p.Description != "" {
		header += "\n\n" + p.Description
	}
	inner := []discordgo.MessageComponent{discordgo.TextDisplay{Content: truncate(header, embedDescMax)}}

	if dest := h.destinationBlock(ctx, serverID, p.Request); dest != "" {
		inner = append(inner, divider(), discordgo.TextDisplay{Content: truncate(dest, embedDescMax)})
	}

	if len(p.Items) == 0 {
		inner = append(inner, divider(), discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "supply.embed.no_items", nil)})
	} else {
		inner = append(inner, divider())
		shown, overflow := p.Items, 0
		if len(shown) > postItemsMax {
			overflow = len(shown) - postItemsMax
			shown = shown[:postItemsMax]
		}
		for _, it := range shown {
			name := truncate(h.itemDisplay(ctx, serverID, it.GDID, it.GDVersion), embedTitleMax)
			block := "**" + name + "**\n" + h.itemFieldValue(ctx, serverID, it)
			inner = append(inner, discordgo.TextDisplay{Content: truncate(block, embedDescMax)})
		}
		if overflow > 0 {
			inner = append(inner, discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "supply.embed.items_more", map[string]any{"Count": overflow})})
		}
	}

	footer := h.loc.Render(ctx, serverID, "supply.embed.updated_footer", nil) + " " + fmt.Sprintf("<t:%d:R>", p.UpdatedAt.Unix())
	inner = append(inner, divider(), discordgo.TextDisplay{Content: footer})

	if withButtons {
		inner = append(inner, divider())
		inner = append(inner, h.panelComponents(ctx, serverID)...)
	}
	return []discordgo.MessageComponent{discordgo.Container{Components: inner}}
}

// destinationBlock renders the optional destination lines, one per set fact:
// the gamedata delivery location, a free-text system (name/code), the planet
// (Roman), and the reference-message link. "" when none are set.
func (h *Feature) destinationBlock(ctx context.Context, serverID uuid.UUID, req Request) string {
	var lines []string
	if req.LocationGDID != "" {
		lines = append(lines, h.loc.Render(ctx, serverID, "supply.embed.location_line", map[string]any{
			"Location": h.spaceObjectDisplay(ctx, serverID, req.LocationGDID, req.LocationGDVersion),
		}))
	}
	if sys := systemText(req.SystemName, req.SystemCode); sys != "" {
		lines = append(lines, h.loc.Render(ctx, serverID, "supply.embed.system_line", map[string]any{"System": sys}))
	}
	if req.PlanetNumber != nil {
		lines = append(lines, h.loc.Render(ctx, serverID, "supply.embed.planet_line", map[string]any{
			"Planet": roman.Numeral(*req.PlanetNumber),
		}))
	}
	if req.RefMessage != nil {
		lines = append(lines, h.loc.Render(ctx, serverID, "supply.embed.reference_line", map[string]any{
			"Link": req.RefMessage.Link(),
		}))
	}
	return strings.Join(lines, "\n")
}

// systemText renders "name(code)"; when only one of the pair is set it degrades
// to just that one (e.g. "Muvalis" or "QR-439F"); "" when neither is set.
func systemText(name, code string) string {
	switch {
	case name != "" && code != "":
		return name + "(" + code + ")"
	case name != "":
		return name
	case code != "":
		return code
	default:
		return ""
	}
}

// itemFieldValue renders one item's progress line + per-member contributor lines,
// clamped to Discord's field limit (overflow collapses to "+N more").
func (h *Feature) itemFieldValue(ctx context.Context, serverID uuid.UUID, it Item) string {
	itemKey := "supply.embed.item_line"
	if it.OutstandingReserved() <= 0 {
		itemKey = "supply.embed.item_line_done"
	}
	value := h.loc.Render(ctx, serverID, itemKey, map[string]any{
		"Delivered": it.DeliveredQty,
		"Reserved":  it.OutstandingReserved(),
		"Required":  it.RequiredQty,
		"Remaining": it.Remaining(),
	})
	for idx, part := range it.Participants {
		key := "supply.embed.participant_line"
		if part.Outstanding() <= 0 {
			key = "supply.embed.participant_line_done"
		}
		line := "\n" + h.loc.Render(ctx, serverID, key, map[string]any{
			"User":        part.UserID,
			"Outstanding": part.Outstanding(),
			"Delivered":   part.Delivered,
		})
		if utf8.RuneCountInString(value)+utf8.RuneCountInString(line) > embedFieldValueMax-embedOverflowReserve {
			value += "\n" + h.loc.Render(ctx, serverID, "supply.embed.participants_more",
				map[string]any{"Count": len(it.Participants) - idx})
			break
		}
		value += line
	}
	return truncate(value, embedFieldValueMax)
}

// statusLine renders the one-line status.
func (h *Feature) statusLine(ctx context.Context, serverID uuid.UUID, p Progress) string {
	switch p.Status {
	case StatusOpen:
		return h.loc.Render(ctx, serverID, "supply.embed.status_open", nil)
	case StatusCompleted:
		return h.loc.Render(ctx, serverID, "supply.embed.status_completed", nil)
	case StatusCancelled:
		return h.loc.Render(ctx, serverID, "supply.embed.status_cancelled", nil)
	default:
		return ""
	}
}

// --- display delegators to the shared picker ---

func (h *Feature) itemDisplay(ctx context.Context, serverID uuid.UUID, gdid, version string) string {
	return h.pick.ItemDisplay(ctx, serverID, gdid, version)
}

func (h *Feature) spaceObjectDisplay(ctx context.Context, serverID uuid.UUID, gdid, version string) string {
	return h.pick.SpaceObjectDisplay(ctx, serverID, gdid, version)
}

// truncate clips s to at most n runes (Discord counts characters, not bytes).
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
