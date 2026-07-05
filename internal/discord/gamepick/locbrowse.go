package gamepick

import (
	"context"
	"fmt"
	"sort"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// The delivery-location browser: one select over every space object (they
// comfortably fit a single 25-option menu), the current location pre-selected,
// plus Clear and Back. Modal-free: picking applies immediately (locations take
// no quantity). A location destination sets Current + Clear.

// locDest decodes the (destCode, target) prefix of a location-picker CustomID,
// accepting only registered location destinations (those with a Current hook).
func (p *Picker) locDest(parts []string) (Destination, uuid.UUID, bool) {
	if len(parts) < 2 {
		return Destination{}, uuid.Nil, false
	}
	dest, ok := p.dest(parts[0])
	if !ok || dest.Current == nil {
		return Destination{}, uuid.Nil, false
	}
	targetID, ok := argUUID(parts, 1)
	return dest, targetID, ok
}

// RenderLocation is the feature entry point into the delivery-location picker:
// it resolves the destination code (which must be a registered location
// destination) and opens the picker. Feature "set location" handlers delegate
// here.
func (p *Picker) RenderLocation(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, destCode string, targetID uuid.UUID) error {
	dest, ok := p.dest(destCode)
	if !ok || dest.Current == nil {
		return p.notFound(ctx, r, i, serverID)
	}
	return p.renderLocationBrowser(ctx, r, i, serverID, dest, targetID)
}

// renderLocationBrowser transforms the message into the delivery-location
// picker.
func (p *Picker) renderLocationBrowser(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest Destination, targetID uuid.UUID) error {
	cat := p.cfg.Reg.Latest()
	if cat == nil {
		return p.cfg.OnError(ctx, r, i, serverID, fmt.Errorf("gamepick: no gamedata versions loaded"))
	}
	lang := p.langOf(ctx, serverID)

	current, err := dest.Current(ctx, serverID, targetID)
	if err != nil {
		return p.cfg.OnError(ctx, r, i, serverID, err)
	}

	type locRow struct{ id, name string }
	var locs []locRow
	for _, so := range cat.SpaceObjects() {
		name := cat.SpaceObjectName(so.ID, lang)
		if name == "" {
			name = string(so.ID)
		}
		locs = append(locs, locRow{id: string(so.ID), name: name})
	}
	sort.Slice(locs, func(a, b int) bool { return locs[a].name < locs[b].name })
	// A single select holds 25 options; the game ships six stations. Should the
	// catalog ever outgrow that, clamp loudly rather than render an invalid menu.
	if len(locs) > browsePageSize {
		p.cfg.Log.Warn("gamepick: space objects exceed one select; clamping", zap.Int("total", len(locs)))
		locs = locs[:browsePageSize]
	}

	options := make([]discordgo.SelectMenuOption, 0, len(locs))
	for _, l := range locs {
		options = append(options, discordgo.SelectMenuOption{
			Label:   truncate(l.name, 100),
			Value:   l.id,
			Default: l.id == current,
		})
	}

	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: p.key(ctx, serverID, "browse_loc_title", nil)},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
			MenuType:    discordgo.StringSelectMenu,
			CustomID:    p.buildID(segLocBrowse, dest.Code, targetID.String()),
			Placeholder: p.key(ctx, serverID, "pick_placeholder", nil),
			Options:     options,
		}}},
		divider(),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    p.key(ctx, serverID, "btn_loc_clear", nil),
				Style:    discordgo.DangerButton,
				CustomID: p.buildID(segLocClear, dest.Code, targetID.String()),
				Disabled: current == "",
			},
			discordgo.Button{
				Label:    p.key(ctx, serverID, "btn_back", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: dest.BackID(targetID),
			},
		}},
	}
	return r.UpdateComponentsV2(i.Interaction, []discordgo.MessageComponent{discordgo.Container{Components: inner}})
}

// HandleLocBrowse serves the location picker: a select CHOICE applies the picked
// space object (re-authorized per destination); a plain click renders the
// picker.
func (p *Picker) HandleLocBrowse(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := p.locDest(parts)
	if !ok {
		return p.notFound(ctx, r, i, serverID)
	}
	values := i.MessageComponentData().Values
	if len(values) != 1 {
		return p.renderLocationBrowser(ctx, r, i, serverID, dest, targetID)
	}
	if proceed, err := p.authorize(ctx, r, i, serverID, dest, targetID); !proceed {
		return err
	}
	return p.applyPicked(ctx, r, i, serverID, dest, targetID, values[0], 0, true)
}

// HandleLocClear clears the destination's delivery location.
func (p *Picker) HandleLocClear(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	dest, targetID, ok := p.locDest(parts)
	if !ok {
		return p.notFound(ctx, r, i, serverID)
	}
	if proceed, err := p.authorize(ctx, r, i, serverID, dest, targetID); !proceed {
		return err
	}
	return dest.Clear(ctx, r, i, serverID, targetID)
}
