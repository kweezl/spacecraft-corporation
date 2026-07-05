package supply

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/gamepick"
	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
)

// pickDestinations registers the two gamedata-pick destinations with the shared
// picker. Both have Authorize: nil — the ownership boundary is the owner_user_id
// predicate in every Apply's SQL, so a forged id affects zero rows rather than
// being caught by a bot permission check.
func (h *Feature) pickDestinations() []gamepick.Destination {
	backToView := func(t uuid.UUID) string { return buildID(segView, t.String()) }

	return []gamepick.Destination{
		{
			Code: destItem, Kind: gamedata.KindItem, NeedsQty: true, Browse: true,
			BackID: backToView,
			OpenModal: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID) error {
				comps := []discordgo.MessageComponent{h.searchInput(ctx, serverID, "")}
				return r.RespondModal(i.Interaction, buildID(segMIAdd, targetID.String()),
					h.modalTitle(ctx, serverID, "supply.console.modal_additem_title"), comps)
			},
			Apply: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID, p gamepick.Picked, qty int, update bool) error {
				err := h.repo.AddItem(ctx, serverID, invokerID(i), targetID, p.GDID, p.Version, qty, maxItems)
				if errors.Is(err, ErrMaxItems) {
					return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.console.max_items", map[string]any{"Limit": maxItems}))
				}
				if err != nil {
					return h.consoleErr(ctx, r, i, serverID, err)
				}
				return h.renderRequestView(ctx, r, i, serverID, targetID, 0, update)
			},
		},
		{
			Code: destLoc, Kind: gamedata.KindSpaceObject, NeedsQty: false, Browse: false,
			BackID: backToView,
			Current: func(ctx context.Context, serverID, targetID uuid.UUID) (string, error) {
				return h.currentLocation(ctx, serverID, targetID)
			},
			Apply: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID, p gamepick.Picked, _ int, update bool) error {
				if err := h.repo.SetDeliveryLocation(ctx, serverID, invokerID(i), targetID, p.GDID, p.Version); err != nil {
					return h.consoleErr(ctx, r, i, serverID, err)
				}
				return h.renderRequestView(ctx, r, i, serverID, targetID, 0, update)
			},
			Clear: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID) error {
				if err := h.repo.SetDeliveryLocation(ctx, serverID, invokerID(i), targetID, "", ""); err != nil {
					return h.consoleErr(ctx, r, i, serverID, err)
				}
				return h.renderRequestView(ctx, r, i, serverID, targetID, 0, true)
			},
		},
	}
}

// currentLocation loads a request's stored delivery-location gdid ("" = unset)
// for the location browser's pre-selection and Clear-button state. Read-only, so
// it is not owner-scoped (the browser only reads; the Apply/Clear writes are).
func (h *Feature) currentLocation(ctx context.Context, _ uuid.UUID, requestID uuid.UUID) (string, error) {
	prog, err := h.repo.ProgressByID(ctx, requestID)
	if err != nil {
		return "", err
	}
	return prog.LocationGDID, nil
}
