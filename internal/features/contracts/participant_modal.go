package contracts

import (
	"context"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// The participant editor is the single surface for managing one member's stake in
// an item (the "contracts manager" actions). Discord can't put a quantity input
// and several action buttons on one surface, so it is a modal: an action picker
// plus a quantity field. Submitting applies the chosen action against the
// repository. It is reached from the per-participant Edit button in the item view
// (contract:pedit:<itemid>:<userid>) and gated by keyManage; every action also
// requires the contract to still be open (enforced in the repository).
const (
	// pActionInput is the action select's CustomID inside the modal.
	pActionInput = "paction"

	// Participant actions (the select's option values).
	pActDeliver = "deliver" // mark the typed quantity delivered
	// pActSet sets the OUTSTANDING reserve (reserved minus delivered) to the typed
	// quantity, so reserved becomes delivered + quantity. Entering 0 clears the
	// outstanding reserve (reserved → delivered), removing the participation
	// entirely when nothing has been delivered.
	pActSet    = "set"
	pActRemove = "remove" // remove the participation entirely
)

// parseReserveQty parses the outstanding-reserve input: a whole number ≥ 0 (0 is
// valid — it clears the outstanding reserve). Distinct from parseQty, which
// requires a strictly positive amount for deliver.
func parseReserveQty(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0, errBadQty
	}
	return n, nil
}

// findItemParticipant locates an item and one member's participation within a
// loaded Progress.
func findItemParticipant(prog Progress, itemID uuid.UUID, userID string) (Item, Participant, bool) {
	for _, it := range prog.Items {
		if it.ID != itemID {
			continue
		}
		for _, p := range it.Participants {
			if p.UserID == userID {
				return it, p, true
			}
		}
		return it, Participant{}, false
	}
	return Item{}, Participant{}, false
}

// reserveCap is the most a member may reserve on an item: the required quantity
// minus everyone else's reservations (so total reserved never exceeds required).
func reserveCap(it Item, p Participant) int {
	c := it.RequiredQty - (it.ReservedQty - p.Reserved)
	if c < 0 {
		return 0
	}
	return c
}

func (h *Feature) openParticipantModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok || len(parts) < 2 {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	userID := parts[1]
	prog, err := h.repo.ProgressByItemScoped(ctx, serverID, itemID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	if prog.Status != StatusOpen {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.closed", nil))
	}
	item, p, found := findItemParticipant(prog, itemID, userID)
	if !found {
		return h.consoleErr(ctx, r, i, serverID, ErrNoReservation)
	}

	// OutCap is the most the OUTSTANDING reserve can be set to: the item's spare
	// capacity for this member (gross cap minus what they've already delivered).
	outCap := reserveCap(item, p) - p.Delivered
	if outCap < 0 {
		outCap = 0
	}
	figures := map[string]any{
		"Outstanding": p.Outstanding(), "Delivered": p.Delivered, "OutCap": outCap,
	}
	opts := h.participantActionOptions(ctx, serverID, p, figures)
	title := h.loc.Render(ctx, serverID, "contracts.console.modal_participant_title",
		map[string]any{"Contract": prog.Title, "Item": item.Name})

	comps := []discordgo.MessageComponent{
		discordgo.Label{
			Label: h.loc.Render(ctx, serverID, "contracts.console.pmanage_action", nil),
			Component: discordgo.SelectMenu{
				MenuType:    discordgo.StringSelectMenu,
				CustomID:    pActionInput,
				Placeholder: h.loc.Render(ctx, serverID, "contracts.console.pmanage_action_placeholder", nil),
				MinValues:   intPtr(1),
				MaxValues:   1,
				Options:     opts,
			},
		},
		discordgo.Label{
			Label: h.loc.Render(ctx, serverID, "contracts.console.pmanage_qty", figures),
			Component: discordgo.TextInput{
				CustomID:  inQty,
				Style:     discordgo.TextInputShort,
				Required:  boolPtr(false),
				MaxLength: 12,
			},
		},
	}
	return r.RespondModal(i.Interaction, buildID(segMPEdit, itemID.String(), userID), truncate(title, modalTitleMax), comps)
}

// participantActionOptions builds the action choices, omitting Deliver when the
// member has nothing outstanding to deliver. Update reserve (set to 0 to clear)
// subsumes the old "cancel reserve" action.
func (h *Feature) participantActionOptions(ctx context.Context, serverID uuid.UUID, p Participant, figures map[string]any) []discordgo.SelectMenuOption {
	opt := func(value, labelKey, descKey string) discordgo.SelectMenuOption {
		return discordgo.SelectMenuOption{
			Label:       h.loc.Render(ctx, serverID, labelKey, nil),
			Value:       value,
			Description: truncate(h.loc.Render(ctx, serverID, descKey, figures), 100),
		}
	}
	var opts []discordgo.SelectMenuOption
	if p.Outstanding() > 0 {
		opts = append(opts, opt(pActDeliver, "contracts.console.pact_deliver", "contracts.console.pact_deliver_desc"))
	}
	opts = append(opts, opt(pActSet, "contracts.console.pact_set", "contracts.console.pact_set_desc"))
	opts = append(opts, opt(pActRemove, "contracts.console.pact_remove", "contracts.console.pact_remove_desc"))
	return opts
}

func (h *Feature) submitParticipant(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok || len(parts) < 2 {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	userID := parts[1]
	data := i.ModalSubmitData()
	action := modalSelectValue(data, pActionInput)
	actor := invokerID(i)

	switch action {
	case pActDeliver:
		qty, qerr := parseQty(modalTextValue(data, inQty))
		if qerr != nil {
			return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_qty", nil))
		}
		cid, complete, err := h.repo.DeliverByItem(ctx, serverID, itemID, userID, qty, actor)
		if err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
		if complete {
			return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
		}
	case pActSet:
		// The input is the desired OUTSTANDING reserve; the new total reserved is
		// delivered + outstanding. Resolve delivered from live state (in the same
		// scoped load) so the absolute value handed to the repository is correct;
		// the repository re-validates the floor and cap in its transaction.
		out, qerr := parseReserveQty(modalTextValue(data, inQty))
		if qerr != nil {
			return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_qty", nil))
		}
		prog, err := h.repo.ProgressByItemScoped(ctx, serverID, itemID)
		if err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
		_, p, found := findItemParticipant(prog, itemID, userID)
		if !found {
			return h.consoleErr(ctx, r, i, serverID, ErrNoReservation)
		}
		if _, err := h.repo.SetReservationByItem(ctx, serverID, itemID, userID, p.Delivered+out, actor); err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
	case pActRemove:
		if _, err := h.repo.RemoveReservation(ctx, serverID, itemID, userID, actor); err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
	default:
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.pmanage_bad_action", nil))
	}
	return h.renderItemView(ctx, r, i, serverID, itemID, 0, true)
}
