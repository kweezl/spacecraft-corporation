package gamepick

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
)

// pickMaxHits caps the hits offered in the pick select — well under Discord's
// 25-option limit, keeps the ephemeral tidy.
const pickMaxHits = 10

// RunPick executes a search-modal submit: search the catalog in the server's
// language and dispatch on the hit count — nothing found (ephemeral notice, the
// caller's view is untouched so the user just retries), exactly one for a
// qty-less destination (apply + re-render in place), or otherwise transform the
// message into the pick page. A single hit for an ITEM destination still goes
// through the pick page: its quantity is asked in a modal after the pick, and a
// modal may not open from this (modal-submit) interaction — the one-option
// select is the bridge.
func (p *Picker) RunPick(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, destCode string, targetID uuid.UUID, query string) error {
	dest, ok := p.dest(destCode)
	if !ok {
		return fmt.Errorf("gamepick: unknown dest %q", destCode)
	}
	hits, err := p.cfg.Search.Search(dest.Kind, p.langOf(ctx, serverID), query, pickMaxHits)
	if err != nil {
		return p.cfg.OnError(ctx, r, i, serverID, err)
	}
	// Excluded categories never surface as item hits (they can't be requirements;
	// applyPicked rejects them as the hard boundary).
	if dest.Kind == gamedata.KindItem {
		kept := hits[:0]
		for _, hit := range hits {
			if p.Pickable(hit.ID) {
				kept = append(kept, hit)
			}
		}
		hits = kept
	}
	if len(hits) == 0 {
		return r.RespondEphemeral(i.Interaction, p.key(ctx, serverID, "pick_none", map[string]any{"Query": query}))
	}
	if len(hits) == 1 && !dest.NeedsQty {
		return p.applyPicked(ctx, r, i, serverID, dest, targetID, string(hits[0].ID), 0, true)
	}

	options := make([]discordgo.SelectMenuOption, 0, len(hits))
	for _, hit := range hits {
		options = append(options, discordgo.SelectMenuOption{
			Label: truncate(hit.Name, 100),
			Value: string(hit.ID),
			// The item's icon renders inline in the dropdown — select options
			// support custom emojis, the one Discord surface where icons and a
			// pick list combine.
			Emoji: p.OptionEmoji(hit.ID),
		})
	}
	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: p.key(ctx, serverID, "pick_title", map[string]any{"Query": query})},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
			MenuType:    discordgo.StringSelectMenu,
			CustomID:    p.buildID(segPick, dest.Code, targetID.String()),
			Placeholder: p.key(ctx, serverID, "pick_placeholder", nil),
			Options:     options,
		}}},
		divider(),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    p.key(ctx, serverID, "btn_search", nil),
				Style:    discordgo.PrimaryButton,
				CustomID: p.pickSearchID(dest, targetID),
			},
			discordgo.Button{
				Label:    p.key(ctx, serverID, "btn_back", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: dest.BackID(targetID),
			},
		}},
	}
	// Update IN PLACE: the message the modal was opened from becomes the pick
	// page, so the user's focus stays where they were working.
	return r.UpdateComponentsV2(i.Interaction, []discordgo.MessageComponent{discordgo.Container{Components: inner}})
}

// pickSearchID is the pick page's Search button: browse destinations reopen the
// modal-free browser's query modal (a picker-owned segment), while non-browse
// ones (legacy-item linking) use the destination's own search opener id.
func (p *Picker) pickSearchID(dest Destination, targetID uuid.UUID) string {
	if dest.Browse {
		return p.buildID(segBrowseSearch, dest.Code, targetID.String())
	}
	return dest.SearchID(targetID)
}

// HandlePick handles the choice made in the pick select: item destinations
// continue to the quantity modal (this is a component interaction, so a modal
// may open), the rest apply immediately. This segment isn't a fixed console
// button, so it re-checks the destination's Authorizer here.
func (p *Picker) HandlePick(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	if len(parts) < 2 {
		return fmt.Errorf("gamepick: malformed pick id %v", parts)
	}
	dest, ok := p.dest(parts[0])
	if !ok {
		return p.notFound(ctx, r, i, serverID)
	}
	targetID, ok := argUUID(parts, 1)
	if !ok {
		return p.notFound(ctx, r, i, serverID)
	}
	if proceed, err := p.authorize(ctx, r, i, serverID, dest, targetID); !proceed {
		return err
	}
	values := i.MessageComponentData().Values
	if len(values) != 1 {
		return fmt.Errorf("gamepick: pick select expects one value, got %d", len(values))
	}
	if dest.NeedsQty {
		return p.openQtyModal(ctx, r, i, serverID, dest, targetID, values[0])
	}
	return p.applyPicked(ctx, r, i, serverID, dest, targetID, values[0], 0, true)
}

// authorize runs the destination's Authorizer (nil = allowed). proceed=false
// means it already responded.
func (p *Picker) authorize(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest Destination, targetID uuid.UUID) (bool, error) {
	if dest.Authorize == nil {
		return true, nil
	}
	return dest.Authorize(ctx, r, i, serverID, targetID)
}

// applyPicked snapshots the picked object against the latest catalog and hands
// it to the destination's Apply. New links are stamped with the latest loaded
// version; item destinations also snapshot the localized name + aliases. The
// excluded-category check is the hard boundary — the gdid arrives via a CustomID,
// which can be forged.
func (p *Picker) applyPicked(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest Destination, targetID uuid.UUID, gdid string, qty int, update bool) error {
	cat := p.cfg.Reg.Latest()
	if cat == nil {
		return p.cfg.OnError(ctx, r, i, serverID, fmt.Errorf("gamepick: no gamedata versions loaded"))
	}
	if dest.Kind == gamedata.KindItem && !p.Pickable(gamedata.GDID(gdid)) {
		return r.RespondEphemeral(i.Interaction, p.key(ctx, serverID, "item_excluded", nil))
	}
	picked := Picked{GDID: gdid, Version: cat.Version()}
	if dest.Kind == gamedata.KindItem {
		name := cat.Name(gamedata.GDID(gdid), p.langOf(ctx, serverID))
		if name == "" {
			name = gdid
		}
		picked.Name = name
		picked.Aliases = p.Aliases(gdid, picked.Version)
	}
	return dest.Apply(ctx, r, i, serverID, targetID, picked, qty, update)
}
