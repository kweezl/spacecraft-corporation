package contracts

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
)

// The button panel lets a member participate/deliver/release straight from the
// contract's public forum post — no slash command, no typing the item name. The
// post carries three buttons; clicking one opens a single modal — a centered
// overlay that is always visible no matter how far the reader has scrolled in a
// long forum thread (an ephemeral message, by contrast, lands at the thread's
// bottom, out of view). That one modal gathers everything: a Label-wrapped item
// select (the items the contract still needs for participate; the member's own
// outstanding reservations for deliver and release) plus a quantity input.
// Submitting it runs the same repository mutation the slash leaf would (so the
// public embed refresh is enqueued for free). With a single eligible item the
// select is pre-selected and the quantity pre-filled, so it is submit-and-go.
//
// The whole flow is stateless: the chosen item rides back in the modal submit, so
// there is no per-user pending state to stash between steps — the submit carries
// op (in its CustomID), item + quantity (its inputs), and the thread/member (the
// interaction itself). The select lives in the modal (not on the post) because the
// post is one shared message — every viewer sees identical components, so "offer
// only the items you still owe" cannot live there; and selects are now allowed
// inside modals (the Label component), so the picker and the amount fit one
// overlay.
//
// CustomID grammar (all under the "contract" namespace the registry routes by):
//
//	contract:panel:<op>  button on the public post (op = participate|deliver|release)
//	contract:qty:<op>    the modal submit (op = participate|deliver|release)
//
// Inside the modal, the select and the quantity input carry the fixed input ids
// modalItemInput / modalQtyInput.
const (
	segPanel = "panel"
	segQty   = "qty"

	// modalItemInput / modalQtyInput are the CustomIDs of the two inputs inside
	// the modal: the item select and the quantity text field.
	modalItemInput = "item"
	modalQtyInput  = "qty"

	// maxSelectOptions is Discord's hard cap on string-select options (also the
	// MaxItems default), so a contract's items always fit one select.
	maxSelectOptions = 25
	// modalTitleMax is Discord's modal-title length cap.
	modalTitleMax = 45
)

// errBadQty is the local parse error for the quantity modal input (not a
// repository sentinel; rendered directly as contracts.panel.bad_qty).
var errBadQty = errors.New("contracts: bad quantity")

// panelComponents builds the action row attached to a contract's progress embed.
// CustomIDs are static (op only) — the handler resolves the contract from the
// thread the interaction lands in, exactly like the in-thread slash leaves.
func (h *Feature) panelComponents(ctx context.Context, serverID uuid.UUID) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.panel.btn_participate", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: panelCustomID(opParticipate),
		},
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.panel.btn_deliver", nil),
			Style:    discordgo.SuccessButton,
			CustomID: panelCustomID(opDeliver),
		},
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.panel.btn_release", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: panelCustomID(opRelease),
		},
	}}}
}

// handleComponent is the entry point for every "contract:" component and modal
// interaction; it routes by interaction type (modal submit) then by the action
// segment of the CustomID.
func (h *Feature) handleComponent(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	if i.Type == discordgo.InteractionModalSubmit {
		return h.handleQtyModal(ctx, r, i, serverID)
	}
	id := i.MessageComponentData().CustomID
	switch segmentOf(id) {
	case "list":
		return h.handleListComponent(ctx, r, i, serverID)
	case segPanel:
		return h.handlePanelButton(ctx, r, i, serverID)
	default:
		return fmt.Errorf("contracts: unknown component id %q", id)
	}
}

// handlePanelButton handles a Participate/Deliver/Release button on the public
// post: it re-authorizes the member against the same per-leaf policy as the slash
// leaf, works out what that op allows (items the contract still needs for
// participate; the member's own outstanding reservations for deliver and release),
// and opens the modal that gathers item + quantity in one overlay. With nothing
// eligible it replies that there is nothing to do. Release is always self-scoped
// here — a manager releasing another member uses the /contract release-member leaf.
func (h *Feature) handlePanelButton(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	op := opOf(i.MessageComponentData().CustomID)
	if op != opParticipate && op != opDeliver && op != opRelease {
		return fmt.Errorf("contracts: bad panel op %q", i.MessageComponentData().CustomID)
	}
	allowed, err := h.authorized(ctx, i, serverID, op)
	if err != nil {
		return fmt.Errorf("contracts: authorize %s: %w", op, err)
	}
	if !allowed {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "session.denied",
			map[string]any{"Command": commandName + " " + op}))
	}

	items, err := h.eligibleItems(ctx, serverID, threadOf(i), invokerID(i), op)
	if err != nil {
		if handled, rerr := h.replyMapped(ctx, r, i, serverID, err); handled {
			return rerr
		}
		return fmt.Errorf("contracts: panel %s options: %w", op, err)
	}
	if len(items) == 0 {
		key := "contracts.panel.none_participate"
		switch op {
		case opDeliver:
			key = "contracts.panel.none_deliver"
		case opRelease:
			key = "contracts.panel.none_release"
		}
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, key, nil))
	}
	return h.openOpModal(ctx, r, i, serverID, op, items)
}

// itemAvail is an eligible item plus how much of it the member may act on right
// now (the contract's remaining-unreserved qty for participate, the member's own
// outstanding qty for deliver and release).
type itemAvail struct {
	name  string
	avail int
}

// eligibleItems lists the items a member may act on for an op, with the live
// available quantity: participate offers items the contract still needs
// (Remaining > 0) on an open contract; deliver and release both offer the member's
// own outstanding reservations (Outstanding = reserved − delivered > 0) — the same
// set, since you can only deliver or release what you have reserved and not yet
// delivered. Capped at maxSelectOptions so the result always fits one select.
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
			out = append(out, itemAvail{name: m.Name, avail: m.Outstanding()})
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
	if prog.Status != StatusOpen { // a closed contract accepts no participation
		return nil, ErrClosed
	}
	for _, it := range prog.Items {
		if it.Remaining() <= 0 {
			continue
		}
		out = append(out, itemAvail{name: it.Name, avail: it.Remaining()})
		if len(out) >= maxSelectOptions {
			break
		}
	}
	return out, nil
}

// openOpModal opens the one modal that gathers the whole op: a Label-wrapped item
// select over the eligible items plus a quantity input, submitted together as
// contract:qty:<op>. With a single eligible item the lone option is pre-selected
// and the quantity pre-filled with its available amount (remaining-to-reserve for
// participate; outstanding = reserved − delivered for deliver/release), so the
// common case is submit-and-go. With several items nothing is pre-selected and the
// quantity is left blank: a modal is static, so a pre-filled amount cannot track
// the picked item — switching the select would leave a stale amount (e.g. 10 for
// iron when copper has only 5), inviting an over-delivery. The member instead picks
// one and types the amount, guided by each option's description (which shows its
// available qty).
//
// Because the modal is a centered overlay it is always on screen, unlike an
// ephemeral follow-up that would land at the thread's bottom.
func (h *Feature) openOpModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, op string, items []itemAvail) error {
	pickKey := "contracts.panel.pick_participate"
	descKey := "contracts.panel.opt_participate"
	titleKey := "contracts.panel.modal_participate"
	amountKey := "Remaining"
	switch op {
	case opDeliver:
		pickKey = "contracts.panel.pick_deliver"
		descKey = "contracts.panel.opt_deliver"
		titleKey = "contracts.panel.modal_deliver"
		amountKey = "Outstanding"
	case opRelease:
		pickKey = "contracts.panel.pick_release"
		descKey = "contracts.panel.opt_release"
		titleKey = "contracts.panel.modal_release"
		amountKey = "Outstanding"
	}

	// prefill: pre-select the option and pre-fill its amount only when there is a
	// single eligible item — then the choice is unambiguous and the modal is
	// submit-and-go. With several items the static quantity field cannot follow the
	// picked select, so leave both unset (the member picks one and types).
	prefill := len(items) == 1
	opts := make([]discordgo.SelectMenuOption, 0, len(items))
	for idx, it := range items {
		o := h.itemOption(ctx, serverID, it.name, descKey, map[string]any{amountKey: it.avail})
		if prefill && idx == 0 { // pre-select the default so the modal is submit-and-go
			o.Default = true
		}
		opts = append(opts, o)
	}

	qty := discordgo.TextInput{
		CustomID:    modalQtyInput,
		Style:       discordgo.TextInputShort,
		Placeholder: h.loc.Render(ctx, serverID, "contracts.panel.qty_placeholder", nil),
		Required:    boolPtr(true),
		MaxLength:   12,
	}
	if prefill { // we know the default item's amount up front, so pre-fill it
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
			Label:     h.loc.Render(ctx, serverID, "contracts.panel.qty_label", nil),
			Component: qty,
		},
	}
	title := h.loc.Render(ctx, serverID, titleKey, nil)
	return r.RespondModal(i.Interaction, qtyCustomID(op), truncate(title, modalTitleMax), components)
}

// defaultedItem returns the item the modal would have pre-selected by default,
// re-derived from live state — the stateless fallback for a submit-and-go where the
// client did not echo the untouched default back in an empty select. It mirrors
// openOpModal's pre-selection rule exactly: a default exists only when a single
// item is eligible. ok is false on any error, when nothing is eligible, or when
// several are (none was pre-selected, so an empty select is ambiguous) — the caller
// then asks the member to pick again rather than guess.
func (h *Feature) defaultedItem(ctx context.Context, serverID uuid.UUID, threadID, userID, op string) (string, bool) {
	items, err := h.eligibleItems(ctx, serverID, threadID, userID, op)
	if err != nil || len(items) != 1 {
		return "", false
	}
	return items[0].name, true
}

// itemOption builds one select option. The item name is both the label and the
// value (the repository keys mutations by name); both are clamped to Discord's
// 100-rune option cap. Item names longer than that won't round-trip, but they are
// far beyond any real in-game item name.
func (h *Feature) itemOption(ctx context.Context, serverID uuid.UUID, name, descKey string, data map[string]any) discordgo.SelectMenuOption {
	return discordgo.SelectMenuOption{
		Label:       truncate(name, 100),
		Value:       truncate(name, 100),
		Description: truncate(h.loc.Render(ctx, serverID, descKey, data), 100),
	}
}

// handleQtyModal runs the mutation when the modal is submitted: it re-authorizes
// (the submit is its own interaction, so it mirrors the button's per-leaf gate),
// reads the chosen item from the select and the quantity from the text input, and
// performs the participate/deliver/release against the same repository methods the
// slash leaves use. The contract (thread) and the actor come from the interaction;
// the op rides in the CustomID — no server is threaded between steps.
func (h *Feature) handleQtyModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	data := i.ModalSubmitData()
	op, ok := opOfModal(data.CustomID)
	if !ok {
		return fmt.Errorf("contracts: bad modal id %q", data.CustomID)
	}
	allowed, err := h.authorized(ctx, i, serverID, op)
	if err != nil {
		return fmt.Errorf("contracts: authorize %s: %w", op, err)
	}
	if !allowed {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "session.denied",
			map[string]any{"Command": commandName + " " + op}))
	}

	item := normalizeItem(modalSelectValue(data, modalItemInput))
	if item == "" {
		// The select came back empty, so the member submitted the pre-selected default
		// without touching it (a changed selection is always echoed; only an untouched
		// default's echo is undocumented, so the happy path must not depend on it).
		// Re-derive the default the modal would have pre-selected for this op and use
		// it; if there was none (e.g. participate with several items), ask to pick again.
		if def, ok := h.defaultedItem(ctx, serverID, threadOf(i), invokerID(i), op); ok {
			item = def
		} else {
			return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.panel.expired", nil))
		}
	}
	qty, err := parseQty(modalTextValue(data, modalQtyInput))
	if err != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.panel.bad_qty", nil))
	}

	p := pendingOp{op: op, serverID: serverID, threadID: threadOf(i), userID: invokerID(i), item: item}
	msg, err := h.applyOp(ctx, serverID, p, qty)
	if err != nil {
		if handled, rerr := h.replyMapped(ctx, r, i, serverID, err); handled {
			return rerr
		}
		return fmt.Errorf("contracts: panel %s: %w", op, err)
	}
	return r.RespondEphemeral(i.Interaction, msg)
}

// applyOp runs the participate/deliver/release mutation for a resolved selection
// and returns the localized confirmation. Repository sentinels are returned
// unwrapped so the caller can map them to a user-facing message; an unknown op is a
// programming error. Shared by the button-panel path so it hits the same repository
// methods the slash leaves use. Release is self-scoped here: target and actor are
// both the invoking member (the panel never releases another member's pledge).
func (h *Feature) applyOp(ctx context.Context, serverID uuid.UUID, p pendingOp, qty int) (string, error) {
	switch p.op {
	case opParticipate:
		if err := h.repo.Participate(ctx, p.serverID, p.threadID, p.item, p.userID, qty); err != nil {
			return "", err
		}
		return h.loc.Render(ctx, serverID, "contracts.participate.ok",
			map[string]any{"Item": p.item, "Qty": qty}), nil
	case opDeliver:
		complete, err := h.repo.Deliver(ctx, p.serverID, p.threadID, p.item, p.userID, qty)
		if err != nil {
			return "", err
		}
		key := "contracts.deliver.ok"
		if complete {
			key = "contracts.deliver.completed"
		}
		return h.loc.Render(ctx, serverID, key, map[string]any{"Item": p.item, "Qty": qty}), nil
	case opRelease:
		if err := h.repo.Release(ctx, p.serverID, p.threadID, p.item, p.userID, qty, p.userID); err != nil {
			return "", err
		}
		return h.loc.Render(ctx, serverID, "contracts.release.ok",
			map[string]any{"Item": p.item, "Qty": qty}), nil
	}
	return "", fmt.Errorf("contracts: unknown pending op %q", p.op)
}

// authorized re-checks the interacting member against the per-leaf policy for the
// op ("contract participate" / "contract deliver" / "contract release"), mirroring
// the slash gate:
// administrators bypass; otherwise the member needs a role granted that leaf
// (the leaves are DefaultDeny, matching the command policy). With the permissions
// feature absent (access nil) gating is off entirely, like the session's gate.
func (h *Feature) authorized(ctx context.Context, i *discordgo.InteractionCreate, serverID uuid.UUID, op string) (bool, error) {
	if i.Member != nil && i.Member.Permissions&discordgo.PermissionAdministrator != 0 {
		return true, nil
	}
	if h.access == nil {
		return true, nil
	}
	var roles []string
	if i.Member != nil {
		roles = i.Member.Roles
	}
	return h.access.IsAllowed(ctx, session.AccessRequest{
		ServerID:    serverID,
		Command:     commandName + " " + op,
		UserRoles:   roles,
		DefaultDeny: true,
	})
}

// pendingOp bundles the resolved mutation parameters passed to applyOp.
type pendingOp struct {
	op       string // opParticipate | opDeliver | opRelease
	serverID uuid.UUID
	threadID string
	userID   string
	item     string
}

// --- CustomID helpers ---

func panelCustomID(op string) string { return fmt.Sprintf("%s:%s:%s", componentPrefix, segPanel, op) }
func qtyCustomID(op string) string   { return fmt.Sprintf("%s:%s:%s", componentPrefix, segQty, op) }

// segmentOf returns the action segment (between the 1st and 2nd ':') of a
// CustomID, e.g. "panel" in "contract:panel:participate".
func segmentOf(customID string) string {
	parts := strings.SplitN(customID, ":", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// opOf returns the op segment (the 3rd field) of a panel CustomID.
func opOf(customID string) string {
	parts := strings.SplitN(customID, ":", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

// opOfModal extracts and validates the op from a "contract:qty:<op>" modal id.
func opOfModal(customID string) (string, bool) {
	parts := strings.SplitN(customID, ":", 3)
	if len(parts) != 3 || parts[0] != componentPrefix || parts[1] != segQty {
		return "", false
	}
	switch parts[2] {
	case opParticipate, opDeliver, opRelease:
		return parts[2], true
	}
	return "", false
}

// modalSelectValue reads the single chosen value of the Label-wrapped select with
// the given CustomID from a modal submission (empty if absent/unselected).
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

func boolPtr(b bool) *bool { return &b }
func intPtr(n int) *int    { return &n }
