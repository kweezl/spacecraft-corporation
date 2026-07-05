package gamepick

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// errBadQty is the internal sentinel for a non-positive/non-numeric quantity.
var errBadQty = errors.New("gamepick: quantity must be a positive whole number")

// qtyFieldMaxLen bounds the quantity modal input.
const qtyFieldMaxLen = 12

// openQtyModal prompts for the quantity of a just-picked item — the shared last
// step of both the browse and search flows. The picked gdid rides the modal's
// CustomID (dest:target:gdid).
func (p *Picker) openQtyModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, dest Destination, targetID uuid.UUID, gdid string) error {
	comps := []discordgo.MessageComponent{
		discordgo.Label{
			Label: p.key(ctx, serverID, "lbl_qty", nil),
			Component: discordgo.TextInput{
				CustomID:  qtyField,
				Style:     discordgo.TextInputShort,
				Required:  boolPtr(true),
				MaxLength: qtyFieldMaxLen,
			},
		},
	}
	return r.RespondModal(i.Interaction, p.buildID(segMBrowseQty, dest.Code, targetID.String(), gdid),
		truncate(p.key(ctx, serverID, "modal_qty_title", nil), 45), comps)
}

// HandleQtySubmit applies a picked item with its quantity. This segment carries a
// destination in the CustomID (not a fixed console button), so it re-checks the
// destination's Authorizer here — this is where the browse/search flows finally
// mutate. parts = [destCode, targetID, gdid].
func (p *Picker) HandleQtySubmit(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	if len(parts) < 3 {
		return p.notFound(ctx, r, i, serverID)
	}
	dest, ok := p.dest(parts[0])
	if !ok || !dest.NeedsQty {
		return p.notFound(ctx, r, i, serverID)
	}
	targetID, ok := argUUID(parts, 1)
	if !ok {
		return p.notFound(ctx, r, i, serverID)
	}
	gdid := parts[2]

	if proceed, err := p.authorize(ctx, r, i, serverID, dest, targetID); !proceed {
		return err
	}

	qty, qerr := parseQty(modalTextValue(i.ModalSubmitData(), qtyField))
	if qerr != nil {
		return r.RespondEphemeral(i.Interaction, p.key(ctx, serverID, "bad_qty", nil))
	}
	return p.applyPicked(ctx, r, i, serverID, dest, targetID, gdid, qty, true)
}

// modalTextValue reads the value of the Label-wrapped text input with the given
// CustomID from a modal submission.
func modalTextValue(data discordgo.ModalSubmitInteractionData, customID string) string {
	for _, c := range data.Components {
		label, ok := c.(*discordgo.Label)
		if !ok {
			continue
		}
		if ti, ok := label.Component.(*discordgo.TextInput); ok && ti.CustomID == customID {
			return ti.Value
		}
	}
	return ""
}

// parseQty parses the modal's quantity input: a positive whole number.
func parseQty(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, errBadQty
	}
	return n, nil
}
