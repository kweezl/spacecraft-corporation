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
	comps := append([]discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_name", inName, discordgo.TextInputShort, "", true, titleMaxLen),
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
		ServerID: serverID, Title: title, Deadline: deadline, CreatedByUserID: invokerID(i),
	})
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
}

// --- edit details (name + description) ---

func (h *Feature) openRenameModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	prog, err := h.repo.ProgressByIDScoped(ctx, serverID, cid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_name", inName, discordgo.TextInputShort, prog.Title, true, titleMaxLen),
		h.labelInput(ctx, serverID, "contracts.console.lbl_description", inDesc, discordgo.TextInputParagraph, prog.Description, false, descriptionMaxLen),
	}
	return r.RespondModal(i.Interaction, buildID(segMCName, cid.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_edit_title"), comps)
}

func (h *Feature) submitRename(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	data := i.ModalSubmitData()
	title := normalizeItem(modalTextValue(data, inName))
	if title == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_name", nil))
	}
	desc := strings.TrimSpace(modalTextValue(data, inDesc))
	if err := h.repo.UpdateDetails(ctx, serverID, cid, title, desc, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
}

// --- deadline ---

func (h *Feature) openDeadlineModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
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
	return r.RespondModal(i.Interaction, buildID(segMCDead, cid.String()),
		h.modalTitle(ctx, serverID, "contracts.console.modal_deadline_title"), h.dhmInputs(ctx, serverID, d, hh, m))
}

func (h *Feature) submitDeadline(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	data := i.ModalSubmitData()
	deadline, err := parseDHM(modalTextValue(data, inDays), modalTextValue(data, inHours), modalTextValue(data, inMinutes))
	if err != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_deadline", nil))
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

// --- item rename ---

func (h *Feature) openItemRenameModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	prog, err := h.repo.ProgressByItemScoped(ctx, serverID, itemID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	current := ""
	for _, it := range prog.Items {
		if it.ID == itemID {
			current = it.Name
			break
		}
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_item_name", inName, discordgo.TextInputShort, current, true, 100),
	}
	return r.RespondModal(i.Interaction, buildID(segMIName, itemID.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_item_rename_title"), comps)
}

func (h *Feature) submitItemRename(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	name := normalizeItem(modalTextValue(i.ModalSubmitData(), inName))
	if name == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_name", nil))
	}
	if _, err := h.repo.UpdateItemName(ctx, serverID, itemID, name, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderItemView(ctx, r, i, serverID, itemID, 0, true)
}

// --- release (participant) ---

func (h *Feature) openReleaseModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok || len(parts) < 2 {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	userID := parts[1]
	prog, err := h.repo.ProgressByItemScoped(ctx, serverID, itemID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	prefill := ""
	for _, it := range prog.Items {
		if it.ID != itemID {
			continue
		}
		for _, p := range it.Participants {
			if p.UserID == userID && p.Outstanding() > 0 {
				prefill = strconv.Itoa(p.Outstanding())
			}
		}
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_qty", inQty, discordgo.TextInputShort, prefill, true, 12),
	}
	return r.RespondModal(i.Interaction, buildID(segMPRel, itemID.String(), userID), h.modalTitle(ctx, serverID, "contracts.console.modal_release_title"), comps)
}

func (h *Feature) submitRelease(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok || len(parts) < 2 {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	userID := parts[1]
	qty, qerr := parseQty(modalTextValue(i.ModalSubmitData(), inQty))
	if qerr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_qty", nil))
	}
	if _, err := h.repo.ReleaseByItem(ctx, serverID, itemID, userID, qty, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderItemView(ctx, r, i, serverID, itemID, 0, true)
}

// --- destructive confirmations (cancel / remove item / remove participant) ---

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

func (h *Feature) openRemoveParticipantModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok || len(parts) < 2 {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	return r.RespondModal(i.Interaction, buildID(segMPRem, itemID.String(), parts[1]),
		h.modalTitle(ctx, serverID, "contracts.console.modal_remove_participant_title"),
		[]discordgo.MessageComponent{h.confirmInput(ctx, serverID)})
}

func (h *Feature) submitRemoveParticipant(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok || len(parts) < 2 {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	if !h.confirmed(ctx, serverID, i.ModalSubmitData()) {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.confirm_mismatch", nil))
	}
	if _, err := h.repo.RemoveReservation(ctx, serverID, itemID, parts[1], invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderItemView(ctx, r, i, serverID, itemID, 0, true)
}
