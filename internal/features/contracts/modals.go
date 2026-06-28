package contracts

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// Input length caps and modal input ids.
const (
	titleMaxLen       = 100
	descriptionMaxLen = 2000
	dhmFieldMaxLen    = 4

	inName    = "name"
	inDesc    = "description"
	inDays    = "days"
	inHours   = "hours"
	inMinutes = "minutes"
	inQty     = "qty"
	inConfirm = "confirm"
)

// labelInput builds a Label-wrapped text input (the modal layout the panel uses).
func (h *Feature) labelInput(ctx context.Context, serverID uuid.UUID, labelKey, customID string, style discordgo.TextInputStyle, value string, required bool, maxLen int) discordgo.Label {
	return discordgo.Label{
		Label: h.loc.Render(ctx, serverID, labelKey, nil),
		Component: discordgo.TextInput{
			CustomID:  customID,
			Style:     style,
			Value:     value,
			Required:  boolPtr(required),
			MaxLength: maxLen,
		},
	}
}

// dhmInputs builds the three day/hour/minute inputs, prefilled with d/h/m.
func (h *Feature) dhmInputs(ctx context.Context, serverID uuid.UUID, d, hh, m string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_days", inDays, discordgo.TextInputShort, d, false, dhmFieldMaxLen),
		h.labelInput(ctx, serverID, "contracts.console.lbl_hours", inHours, discordgo.TextInputShort, hh, false, dhmFieldMaxLen),
		h.labelInput(ctx, serverID, "contracts.console.lbl_minutes", inMinutes, discordgo.TextInputShort, m, false, dhmFieldMaxLen),
	}
}

// modalTitle renders a modal title clamped to Discord's limit.
func (h *Feature) modalTitle(ctx context.Context, serverID uuid.UUID, key string) string {
	return truncate(h.loc.Render(ctx, serverID, key, nil), modalTitleMax)
}

// --- create ---

func (h *Feature) openCreateModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	// Custom create captures all up-front fields (name, description, deadline);
	// items are added afterward from the contract view. The future template path
	// will instead pick a template and prompt only for the deadline.
	comps := append([]discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_name", inName, discordgo.TextInputShort, "", true, titleMaxLen),
		h.labelInput(ctx, serverID, "contracts.console.lbl_description", inDesc, discordgo.TextInputParagraph, "", false, descriptionMaxLen),
	}, h.dhmInputs(ctx, serverID, "", "", "")...)
	return r.RespondModal(i.Interaction, buildID(segMCreate), h.modalTitle(ctx, serverID, "contracts.console.modal_create_title"), comps)
}

func (h *Feature) submitCreate(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	data := i.ModalSubmitData()
	title := normalizeItem(modalTextValue(data, inName))
	if title == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_name", nil))
	}
	deadline, err := parseDHM(modalTextValue(data, inDays), modalTextValue(data, inHours), modalTextValue(data, inMinutes))
	if err != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_deadline", nil))
	}
	if _, ok := h.forum.ContractsForumChannelID(ctx, serverID); !ok {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.no_forum", nil))
	}
	// No AppID/Token: the console navigates to the new contract itself, so the
	// worker must NOT edit this interaction's response (it would clobber the view).
	cid, err := h.repo.Create(ctx, CreateInput{
		ServerID: serverID, Kind: KindCustom, Title: title, Description: strings.TrimSpace(modalTextValue(data, inDesc)),
		Deadline: deadline, CreatedByUserID: invokerID(i),
	})
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
}

// --- edit (name + description + deadline; template: deadline only) ---

func (h *Feature) openEditModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	prog, err := h.repo.ProgressByIDScoped(ctx, serverID, cid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	var d, hh, m string
	if prog.Deadline != nil {
		dd, hrs, mins := splitDHM(time.Until(*prog.Deadline))
		d, hh, m = strconv.Itoa(dd), strconv.Itoa(hrs), strconv.Itoa(mins)
	}
	// A template contract's items are fixed, so its edit form is the deadline only;
	// a custom contract edits name + description + deadline in one form.
	var comps []discordgo.MessageComponent
	if prog.Kind == KindCustom {
		comps = append(comps,
			h.labelInput(ctx, serverID, "contracts.console.lbl_name", inName, discordgo.TextInputShort, prog.Title, true, titleMaxLen),
			h.labelInput(ctx, serverID, "contracts.console.lbl_description", inDesc, discordgo.TextInputParagraph, prog.Description, false, descriptionMaxLen),
		)
	}
	comps = append(comps, h.dhmInputs(ctx, serverID, d, hh, m)...)
	return r.RespondModal(i.Interaction, buildID(segMCEdit, cid.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_edit_title"), comps)
}

func (h *Feature) submitEdit(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	kind, err := h.repo.KindByID(ctx, serverID, cid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	data := i.ModalSubmitData()
	deadline, derr := parseDHM(modalTextValue(data, inDays), modalTextValue(data, inHours), modalTextValue(data, inMinutes))
	if derr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_deadline", nil))
	}
	// Custom contracts also rewrite title + description; template contracts have
	// only a deadline to set.
	if kind == KindCustom {
		title := normalizeItem(modalTextValue(data, inName))
		if title == "" {
			return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_name", nil))
		}
		desc := strings.TrimSpace(modalTextValue(data, inDesc))
		if err := h.repo.UpdateDetails(ctx, serverID, cid, title, desc, invokerID(i)); err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
	}
	if err := h.repo.SetDeadline(ctx, serverID, cid, deadline, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
}

// --- add item ---

func (h *Feature) openAddItemModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_item_name", inName, discordgo.TextInputShort, "", true, 100),
		h.labelInput(ctx, serverID, "contracts.console.lbl_qty", inQty, discordgo.TextInputShort, "", true, 12),
	}
	return r.RespondModal(i.Interaction, buildID(segMCAdd, cid.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_additem_title"), comps)
}

func (h *Feature) submitAddItem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	data := i.ModalSubmitData()
	name := normalizeItem(modalTextValue(data, inName))
	qty, qerr := parseQty(modalTextValue(data, inQty))
	if name == "" || qerr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_item", nil))
	}
	err := h.repo.AddItemByID(ctx, serverID, cid, name, qty, h.cfg.MaxItems, invokerID(i))
	if errors.Is(err, ErrMaxItems) {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.max_items", map[string]any{"Limit": h.cfg.MaxItems}))
	}
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
}

// --- item edit (name + quantity) ---

func (h *Feature) openItemEditModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	prog, err := h.repo.ProgressByItemScoped(ctx, serverID, itemID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	name, qty := "", ""
	for _, it := range prog.Items {
		if it.ID == itemID {
			name, qty = it.Name, strconv.Itoa(it.RequiredQty)
			break
		}
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_item_name", inName, discordgo.TextInputShort, name, true, 100),
		h.labelInput(ctx, serverID, "contracts.console.lbl_qty", inQty, discordgo.TextInputShort, qty, true, 12),
	}
	return r.RespondModal(i.Interaction, buildID(segMIEdit, itemID.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_item_edit_title"), comps)
}

func (h *Feature) submitItemEdit(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	data := i.ModalSubmitData()
	name := normalizeItem(modalTextValue(data, inName))
	qty, qerr := parseQty(modalTextValue(data, inQty))
	if name == "" || qerr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_item", nil))
	}
	if _, err := h.repo.UpdateItem(ctx, serverID, itemID, name, qty, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderItemView(ctx, r, i, serverID, itemID, 0, true)
}

// --- destructive confirmations (cancel contract / remove item) ---

// confirmInput is the single confirmation field shared by the destructive modals:
// the member must type the confirm word.
func (h *Feature) confirmInput(ctx context.Context, serverID uuid.UUID) discordgo.MessageComponent {
	want := h.loc.Render(ctx, serverID, "contracts.console.confirm_word", nil)
	return discordgo.Label{
		Label: h.loc.Render(ctx, serverID, "contracts.console.confirm_label", map[string]any{"Word": want}),
		Component: discordgo.TextInput{
			CustomID:  inConfirm,
			Style:     discordgo.TextInputShort,
			Required:  boolPtr(true),
			MaxLength: 32,
		},
	}
}

// confirmed reports whether the submitted confirm field matches the confirm word.
func (h *Feature) confirmed(ctx context.Context, serverID uuid.UUID, data discordgo.ModalSubmitInteractionData) bool {
	want := h.loc.Render(ctx, serverID, "contracts.console.confirm_word", nil)
	return strings.EqualFold(strings.TrimSpace(modalTextValue(data, inConfirm)), want)
}

func (h *Feature) openCancelModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return r.RespondModal(i.Interaction, buildID(segMCancel, cid.String()),
		h.modalTitle(ctx, serverID, "contracts.console.modal_cancel_title"),
		[]discordgo.MessageComponent{h.confirmInput(ctx, serverID)})
}

func (h *Feature) submitCancel(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	if !h.confirmed(ctx, serverID, i.ModalSubmitData()) {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.confirm_mismatch", nil))
	}
	if err := h.repo.CancelByID(ctx, serverID, cid, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderListView(ctx, r, i, serverID, defaultMask, 0, true)
}

func (h *Feature) openRemoveItemModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	return r.RespondModal(i.Interaction, buildID(segMIDel, itemID.String()),
		h.modalTitle(ctx, serverID, "contracts.console.modal_remove_item_title"),
		[]discordgo.MessageComponent{h.confirmInput(ctx, serverID)})
}

func (h *Feature) submitRemoveItem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	if !h.confirmed(ctx, serverID, i.ModalSubmitData()) {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.confirm_mismatch", nil))
	}
	cid, _, err := h.repo.RemoveItemByID(ctx, serverID, itemID, invokerID(i))
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
}
