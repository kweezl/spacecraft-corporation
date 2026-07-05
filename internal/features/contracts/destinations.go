package contracts

import (
	"context"
	"errors"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/gamepick"
	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
)

// pickDest routes an applied pick to its destination write + re-render. The
// value rides in the picker CustomID. Kept as typed consts so pickBackID /
// pickSearchID (pinned by picker_pinning_test.go) and the destination
// registrations read the same way they did before the gamepick extraction.
type pickDest string

const (
	pickContractItem pickDest = "ci" // AddItemByID on a contract
	pickTemplateItem pickDest = "ti" // AddTemplateItem on a template
	pickTemplateLoc  pickDest = "tl" // SetTemplateLocation on a template
	pickContractLoc  pickDest = "cl" // SetDeliveryLocation on a contract
	pickItemLink     pickDest = "il" // LinkItemGDID on a legacy free-text item
)

// pickBackID is the navigation CustomID that abandons a pick and returns to the
// destination's view. Also the location browser's Back target.
func pickBackID(dest pickDest, targetID uuid.UUID) string {
	switch dest {
	case pickItemLink:
		return buildID(segIRow, targetID.String())
	case pickTemplateItem, pickTemplateLoc:
		return buildID(segTView, targetID.String(), "0")
	default: // contract item / location
		return buildID(segView, targetID.String())
	}
}

// pickSearchID is the CustomID that reopens the destination's search modal, so
// refining a query needs no navigation. Only the searching destinations reach
// the pick page — locations are picked from the modal-free browser and never do.
func pickSearchID(dest pickDest, targetID uuid.UUID) string {
	if dest == pickItemLink {
		return buildID(segILink, targetID.String())
	}
	// contract / template item: the browse page's search opener.
	return buildID(segBrowseSearch, string(dest), targetID.String())
}

// maxItemsFor resolves the server's per-contract item cap, falling back to
// DefaultMaxItems when the server has not set one.
func (h *Feature) maxItemsFor(ctx context.Context, serverID uuid.UUID) int {
	if limit, ok := h.itemCap.ContractsMaxItems(ctx, serverID); ok {
		return limit
	}
	return DefaultMaxItems
}

// pickDestinations registers the five gamedata-pick destinations with the shared
// picker. Each closure wraps the write + re-render the pre-extraction applyPick
// switch did, plus the manager-key re-check every pick apply performs.
func (h *Feature) pickDestinations() []gamepick.Destination {
	// authorize re-checks the manager key for a pick apply — these segments carry
	// a destination in the CustomID, so they aren't in gatedSegments.
	authorize := func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, _ uuid.UUID) (bool, error) {
		allowed, err := h.authorizedKey(ctx, i, serverID, keyManage)
		if err != nil {
			return false, fmt.Errorf("contracts: authorize %s: %w", keyManage, err)
		}
		if !allowed {
			return false, h.reply(ctx, r, i, serverID, "contracts.console.denied", nil)
		}
		return true, nil
	}
	backID := func(d pickDest) gamepick.NavID {
		return func(t uuid.UUID) string { return pickBackID(d, t) }
	}
	// openSearch opens the item destination's one-field query modal from the
	// browser's Search button (segMCAdd for contracts, segMTAdd for templates).
	openSearch := func(seg string) gamepick.ModalOpener {
		return func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID) error {
			comps := []discordgo.MessageComponent{
				h.searchInput(ctx, serverID, "contracts.console.search_hint", "", true),
			}
			return r.RespondModal(i.Interaction, buildID(seg, targetID.String()),
				h.modalTitle(ctx, serverID, "contracts.console.modal_additem_title"), comps)
		}
	}

	return []gamepick.Destination{
		{
			Code: string(pickContractItem), Kind: gamedata.KindItem, NeedsQty: true, Browse: true,
			Authorize: authorize,
			BackID:    backID(pickContractItem),
			OpenModal: openSearch(segMCAdd),
			Apply: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID, p gamepick.Picked, qty int, update bool) error {
				limit := h.maxItemsFor(ctx, serverID)
				err := h.repo.AddItemByID(ctx, serverID, targetID, p.Name, p.GDID, p.Version, p.Aliases, qty, limit, invokerID(i))
				if errors.Is(err, ErrMaxItems) {
					return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.max_items", map[string]any{"Limit": limit}))
				}
				if err != nil {
					return h.consoleErr(ctx, r, i, serverID, err)
				}
				return h.renderContractView(ctx, r, i, serverID, targetID, 0, update)
			},
		},
		{
			Code: string(pickTemplateItem), Kind: gamedata.KindItem, NeedsQty: true, Browse: true,
			Authorize: authorize,
			BackID:    backID(pickTemplateItem),
			OpenModal: openSearch(segMTAdd),
			Apply: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID, p gamepick.Picked, qty int, update bool) error {
				limit := h.maxItemsFor(ctx, serverID)
				err := h.tpls.AddTemplateItem(ctx, serverID, targetID, p.GDID, p.Version, qty, limit, invokerID(i))
				if errors.Is(err, ErrMaxItems) {
					return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.max_items", map[string]any{"Limit": limit}))
				}
				if err != nil {
					return h.consoleErr(ctx, r, i, serverID, err)
				}
				return h.renderTemplateEditView(ctx, r, i, serverID, targetID, 0, update)
			},
		},
		{
			Code: string(pickItemLink), Kind: gamedata.KindItem, NeedsQty: false, Browse: false,
			Authorize: authorize,
			BackID:    backID(pickItemLink),
			SearchID:  func(t uuid.UUID) string { return pickSearchID(pickItemLink, t) },
			Apply: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID, p gamepick.Picked, _ int, update bool) error {
				if _, err := h.repo.LinkItemGDID(ctx, serverID, targetID, p.GDID, p.Version, p.Aliases, invokerID(i)); err != nil {
					return h.consoleErr(ctx, r, i, serverID, err)
				}
				return h.renderItemView(ctx, r, i, serverID, targetID, 0, update)
			},
		},
		{
			Code: string(pickContractLoc), Kind: gamedata.KindSpaceObject, NeedsQty: false, Browse: false,
			Authorize: authorize,
			BackID:    backID(pickContractLoc),
			Current: func(ctx context.Context, serverID, targetID uuid.UUID) (string, error) {
				prog, err := h.repo.ProgressByIDScoped(ctx, serverID, targetID)
				if err != nil {
					return "", err
				}
				return prog.LocationGDID, nil
			},
			Apply: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID, p gamepick.Picked, _ int, update bool) error {
				if err := h.repo.SetDeliveryLocation(ctx, serverID, targetID, p.GDID, p.Version, invokerID(i)); err != nil {
					return h.consoleErr(ctx, r, i, serverID, err)
				}
				return h.renderContractView(ctx, r, i, serverID, targetID, 0, update)
			},
			Clear: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID) error {
				if err := h.repo.SetDeliveryLocation(ctx, serverID, targetID, "", "", invokerID(i)); err != nil {
					return h.consoleErr(ctx, r, i, serverID, err)
				}
				return h.renderContractView(ctx, r, i, serverID, targetID, 0, true)
			},
		},
		{
			Code: string(pickTemplateLoc), Kind: gamedata.KindSpaceObject, NeedsQty: false, Browse: false,
			Authorize: authorize,
			BackID:    backID(pickTemplateLoc),
			Current: func(ctx context.Context, serverID, targetID uuid.UUID) (string, error) {
				t, err := h.tpls.TemplateByID(ctx, serverID, targetID)
				if err != nil {
					return "", err
				}
				return t.LocationGDID, nil
			},
			Apply: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID, p gamepick.Picked, _ int, update bool) error {
				if err := h.tpls.SetTemplateLocation(ctx, serverID, targetID, p.GDID, p.Version, invokerID(i)); err != nil {
					return h.consoleErr(ctx, r, i, serverID, err)
				}
				return h.renderTemplateEditView(ctx, r, i, serverID, targetID, 0, update)
			},
			Clear: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID, targetID uuid.UUID) error {
				if err := h.tpls.SetTemplateLocation(ctx, serverID, targetID, "", "", invokerID(i)); err != nil {
					return h.consoleErr(ctx, r, i, serverID, err)
				}
				return h.renderTemplateEditView(ctx, r, i, serverID, targetID, 0, true)
			},
		},
	}
}
