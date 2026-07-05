package supply

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
)

// The public button panel lets any member reserve/deliver/release straight from
// a request's forum post — no slash command, no typing the item name. The post
// carries three buttons; clicking one opens a single modal (a centered overlay,
// always visible however far the reader has scrolled) with a Label-wrapped item
// select plus a quantity input. It is fully open — no access gate — because a
// supply request is a member asking for help; the boundary that matters (only
// the owner manages the request) lives in the console + SQL, not here.
//
// Unlike contracts, the item select's option VALUE is the item gdid (supply is
// gamedata-native), so the mutation keys by gdid directly.
const (
	// Panel ops ride in the panel button + modal CustomIDs (supply:panel:<op> /
	// supply:qty:<op>).
	opReserve = "reserve"
	opDeliver = "deliver"
	opRelease = "release"

	// modalItemInput / modalQtyInput are the input CustomIDs inside the op modal.
	modalItemInput = "item"
	modalQtyInput  = "qty"

	// maxSelectOptions is Discord's string-select option cap.
	maxSelectOptions = 25
	// modalTitleMax is Discord's modal-title length cap.
	modalTitleMax = 45
)

// errBadQty is the local parse error for a quantity input.
var errBadQty = errors.New("supply: bad quantity")

// threadOf is the channel the interaction landed in — for the panel, the
// request's forum thread.
func threadOf(i *discordgo.InteractionCreate) string { return i.ChannelID }

// invokerID returns the interacting member's Discord user ID (guild-only, so
// Member is set).
func invokerID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	return ""
}

// panelComponents builds the action row attached to the request's card.
func (h *Feature) panelComponents(ctx context.Context, serverID uuid.UUID) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "supply.panel.btn_reserve", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: panelCustomID(opReserve),
		},
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "supply.panel.btn_deliver", nil),
			Style:    discordgo.SuccessButton,
			CustomID: panelCustomID(opDeliver),
		},
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "supply.panel.btn_release", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: panelCustomID(opRelease),
		},
	}}}
}

// itemAvail is an eligible item plus how much of it the member may act on now.
type itemAvail struct {
	gdid      string
	gdVersion string
	avail     int
}

// handlePanelButton opens the op modal (item select + qty) for a
// Reserve/Deliver/Release button. With nothing eligible it says so.
func (h *Feature) handlePanelButton(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	op := opOf(i.MessageComponentData().CustomID)
	if op != opReserve && op != opDeliver && op != opRelease {
		return fmt.Errorf("supply: bad panel op %q", i.MessageComponentData().CustomID)
	}
	items, err := h.eligibleItems(ctx, serverID, threadOf(i), invokerID(i), op)
	if err != nil {
		if key, ok := panelErrorKey(err); ok {
			return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, key, nil))
		}
		return fmt.Errorf("supply: panel %s options: %w", op, err)
	}
	if len(items) == 0 {
		key := "supply.panel.none_reserve"
		switch op {
		case opDeliver:
			key = "supply.panel.none_deliver"
		case opRelease:
			key = "supply.panel.none_release"
		}
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, key, nil))
	}
	return h.openOpModal(ctx, r, i, serverID, op, items)
}

// eligibleItems lists what a member may act on: reserve offers items the request
// still needs (Remaining > 0) on an open request; deliver/release offer the
// member's own outstanding reservations. Capped at one select's worth.
func (h *Feature) eligibleItems(ctx context.Context, serverID uuid.UUID, threadID, userID, op string) ([]itemAvail, error) {
	var out []itemAvail
	if op == opDeliver || op == opRelease {
		items, err := h.repo.MemberOutstanding(ctx, serverID, threadID, userID)
		if err != nil {
			return nil, err
		}
		for _, m := range items {
			if m.Outstanding() <= 0 {
				continue
			}
			out = append(out, itemAvail{gdid: m.GDID, gdVersion: m.GDVersion, avail: m.Outstanding()})
			if len(out) >= maxSelectOptions {
				break
			}
		}
		return out, nil
	}
	prog, err := h.repo.Progress(ctx, serverID, threadID)
	if err != nil {
		return nil, err
	}
	if prog.Status != StatusOpen {
		return nil, ErrClosed
	}
	for _, it := range prog.Items {
		if it.Remaining() <= 0 {
			continue
		}
		out = append(out, itemAvail{gdid: it.GDID, gdVersion: it.GDVersion, avail: it.Remaining()})
		if len(out) >= maxSelectOptions {
			break
		}
	}
	return out, nil
}

// openOpModal opens the op modal: a Label-wrapped item select over the eligible
// items plus a quantity input. With a single item the option is pre-selected and
// its amount pre-filled (submit-and-go); with several, nothing is pre-filled.
func (h *Feature) openOpModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, op string, items []itemAvail) error {
	pickKey, descKey, titleKey := "supply.panel.pick_reserve", "supply.panel.opt_reserve", "supply.panel.modal_reserve"
	switch op {
	case opDeliver:
		pickKey, descKey, titleKey = "supply.panel.pick_deliver", "supply.panel.opt_deliver", "supply.panel.modal_deliver"
	case opRelease:
		pickKey, descKey, titleKey = "supply.panel.pick_release", "supply.panel.opt_release", "supply.panel.modal_release"
	}

	prefill := len(items) == 1
	opts := make([]discordgo.SelectMenuOption, 0, len(items))
	for idx, it := range items {
		o := discordgo.SelectMenuOption{
			Label:       truncate(h.pick.LocalizedItemName(ctx, serverID, it.gdid, it.gdVersion, it.gdid), 100),
			Value:       truncate(it.gdid, 100),
			Description: truncate(h.loc.Render(ctx, serverID, descKey, map[string]any{"Amount": it.avail}), 100),
			Emoji:       h.pick.OptionEmojiFor(gamedata.GDID(it.gdid), it.gdVersion),
		}
		if prefill && idx == 0 {
			o.Default = true
		}
		opts = append(opts, o)
	}

	qty := discordgo.TextInput{
		CustomID:    modalQtyInput,
		Style:       discordgo.TextInputShort,
		Placeholder: h.loc.Render(ctx, serverID, "supply.panel.qty_placeholder", nil),
		Required:    boolPtr(true),
		MaxLength:   qtyMaxLen,
	}
	if prefill {
		qty.Value = strconv.Itoa(items[0].avail)
	}

	components := []discordgo.MessageComponent{
		discordgo.Label{
			Label: h.loc.Render(ctx, serverID, pickKey, nil),
			Component: discordgo.SelectMenu{
				MenuType:  discordgo.StringSelectMenu,
				CustomID:  modalItemInput,
				Options:   opts,
				MinValues: intPtr(1),
				MaxValues: 1,
			},
		},
		discordgo.Label{
			Label:     h.loc.Render(ctx, serverID, "supply.panel.qty_label", nil),
			Component: qty,
		},
	}
	return r.RespondModal(i.Interaction, qtyCustomID(op), truncate(h.loc.Render(ctx, serverID, titleKey, nil), modalTitleMax), components)
}

// defaultedItem re-derives the single eligible item's gdid for a submit-and-go
// whose untouched default select came back empty. ok is false when there is not
// exactly one eligible item (an empty select is then ambiguous).
func (h *Feature) defaultedItem(ctx context.Context, serverID uuid.UUID, threadID, userID, op string) (string, bool) {
	items, err := h.eligibleItems(ctx, serverID, threadID, userID, op)
	if err != nil || len(items) != 1 {
		return "", false
	}
	return items[0].gdid, true
}

// handleQtyModal runs the mutation on modal submit: read the chosen item gdid and
// the quantity, then reserve/deliver/release against the repository.
func (h *Feature) handleQtyModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	data := i.ModalSubmitData()
	op, ok := opOfModal(data.CustomID)
	if !ok {
		return fmt.Errorf("supply: bad modal id %q", data.CustomID)
	}
	gdid := modalSelectValue(data, modalItemInput)
	if gdid == "" {
		if def, ok := h.defaultedItem(ctx, serverID, threadOf(i), invokerID(i), op); ok {
			gdid = def
		} else {
			return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.panel.expired", nil))
		}
	}
	qty, err := parseQty(modalTextValue(data, modalQtyInput))
	if err != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.panel.bad_qty", nil))
	}
	msg, err := h.applyOp(ctx, serverID, op, threadOf(i), gdid, invokerID(i), qty)
	if err != nil {
		if key, ok := panelErrorKey(err); ok {
			return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, key, nil))
		}
		return fmt.Errorf("supply: panel %s: %w", op, err)
	}
	return r.RespondEphemeral(i.Interaction, msg)
}

// applyOp runs the reserve/deliver/release mutation and returns the localized
// confirmation. Repository sentinels are returned unwrapped for panelErrorKey.
func (h *Feature) applyOp(ctx context.Context, serverID uuid.UUID, op, threadID, gdid, userID string, qty int) (string, error) {
	name := h.pick.LocalizedItemName(ctx, serverID, gdid, "", gdid)
	switch op {
	case opReserve:
		if err := h.repo.Reserve(ctx, serverID, threadID, gdid, userID, qty); err != nil {
			return "", err
		}
		return h.loc.Render(ctx, serverID, "supply.reserve.ok", map[string]any{"Item": name, "Qty": qty}), nil
	case opDeliver:
		complete, err := h.repo.Deliver(ctx, serverID, threadID, gdid, userID, qty)
		if err != nil {
			return "", err
		}
		key := "supply.deliver.ok"
		if complete {
			key = "supply.deliver.completed"
		}
		return h.loc.Render(ctx, serverID, key, map[string]any{"Item": name, "Qty": qty}), nil
	case opRelease:
		if err := h.repo.Release(ctx, serverID, threadID, gdid, userID, qty); err != nil {
			return "", err
		}
		return h.loc.Render(ctx, serverID, "supply.release.ok", map[string]any{"Item": name, "Qty": qty}), nil
	}
	return "", fmt.Errorf("supply: unknown panel op %q", op)
}

// panelErrorKey maps a repository sentinel to the public panel's phrasing.
func panelErrorKey(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrNotFound):
		return "supply.error.not_in_thread", true
	case errors.Is(err, ErrClosed):
		return "supply.error.closed", true
	case errors.Is(err, ErrItemNotFound):
		return "supply.error.item_not_found", true
	case errors.Is(err, ErrOverCap):
		return "supply.reserve.over_cap", true
	case errors.Is(err, ErrOverReserved):
		return "supply.deliver.over_reserved", true
	case errors.Is(err, ErrNoReservation):
		return "supply.release.no_reservation", true
	case errors.Is(err, ErrBelowDelivered):
		return "supply.release.below_delivered", true
	default:
		return "", false
	}
}

// --- CustomID + modal-value helpers ---

func panelCustomID(op string) string { return fmt.Sprintf("%s:%s:%s", componentPrefix, segPanel, op) }
func qtyCustomID(op string) string   { return fmt.Sprintf("%s:%s:%s", componentPrefix, segQty, op) }

func opOf(customID string) string {
	parts := strings.SplitN(customID, ":", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

func opOfModal(customID string) (string, bool) {
	parts := strings.SplitN(customID, ":", 3)
	if len(parts) != 3 || parts[0] != componentPrefix || parts[1] != segQty {
		return "", false
	}
	switch parts[2] {
	case opReserve, opDeliver, opRelease:
		return parts[2], true
	}
	return "", false
}

func modalSelectValue(data discordgo.ModalSubmitInteractionData, customID string) string {
	for _, c := range data.Components {
		label, ok := c.(*discordgo.Label)
		if !ok {
			continue
		}
		if sel, ok := label.Component.(*discordgo.SelectMenu); ok && sel.CustomID == customID && len(sel.Values) > 0 {
			return sel.Values[0]
		}
	}
	return ""
}

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

// parseQty parses a positive whole number.
func parseQty(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, errBadQty
	}
	return n, nil
}

// parsePlanet parses the optional planet field: empty → (nil, true); a positive
// whole number → (&n, true); anything else → (nil, false).
func parsePlanet(s string) (*int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return nil, false
	}
	return &n, true
}
