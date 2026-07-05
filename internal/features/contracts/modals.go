package contracts

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

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
	// inQuery is the gamedata search field (item / space-object pickers).
	inQuery = "query"
	// The four reward fields (contract + template rewards modals).
	inCredits    = "credits"
	inReputation = "reputation"
	inLicence    = "licence"
	inFactor     = "factor"

	rewardFieldMaxLen = 13 // NUMERIC(14,2): up to 10 digits + separator + 2
	factorFieldMaxLen = 6  // NUMERIC(5,2): "100.00"
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

// searchInput is a gamedata/title search field: a Label-wrapped text input whose
// description spells out the search-then-pick flow — Discord modals have NO live
// autocomplete (that exists only on slash-command options), so matches can only
// appear after the modal is submitted. hintKey picks the per-flow explanation.
func (h *Feature) searchInput(ctx context.Context, serverID uuid.UUID, hintKey, value string, required bool) discordgo.Label {
	return discordgo.Label{
		Label:       h.loc.Render(ctx, serverID, "contracts.console.lbl_search", nil),
		Description: truncate(h.loc.Render(ctx, serverID, hintKey, nil), 100),
		Component: discordgo.TextInput{
			CustomID:    inQuery,
			Style:       discordgo.TextInputShort,
			Value:       value,
			Placeholder: truncate(h.loc.Render(ctx, serverID, "contracts.console.search_placeholder", nil), 100),
			Required:    boolPtr(required),
			MaxLength:   100,
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
	// items are added afterward from the contract view. The template path instead
	// picks a template and confirms title + deadline (template_modals.go).
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
	// The participant reward factor prefills from the server default (copied, so a
	// later default change never touches this contract; editable in the rewards
	// modal like the rest).
	cid, err := h.repo.Create(ctx, CreateInput{
		ServerID: serverID, Kind: KindCustom, Title: title, Description: strings.TrimSpace(modalTextValue(data, inDesc)),
		Deadline: deadline, ParticipantRewardFactor: h.defaults.ContractsRewardFactor(ctx, serverID),
		CreatedByUserID: invokerID(i),
	})
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
}

// --- edit (name + description + deadline) ---

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
	// Both kinds edit name + description + deadline: a template contract is fully
	// editable — the template only supplied its defaults.
	comps := append([]discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_name", inName, discordgo.TextInputShort, prog.Title, true, titleMaxLen),
		h.labelInput(ctx, serverID, "contracts.console.lbl_description", inDesc, discordgo.TextInputParagraph, prog.Description, false, descriptionMaxLen),
	}, h.dhmInputs(ctx, serverID, d, hh, m)...)
	return r.RespondModal(i.Interaction, buildID(segMCEdit, cid.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_edit_title"), comps)
}

func (h *Feature) submitEdit(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	data := i.ModalSubmitData()
	deadline, derr := parseDHM(modalTextValue(data, inDays), modalTextValue(data, inHours), modalTextValue(data, inMinutes))
	if derr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_deadline", nil))
	}
	title := normalizeItem(modalTextValue(data, inName))
	if title == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_name", nil))
	}
	desc := strings.TrimSpace(modalTextValue(data, inDesc))
	if err := h.repo.UpdateDetails(ctx, serverID, cid, title, desc, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	if err := h.repo.SetDeadline(ctx, serverID, cid, deadline, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
}

// --- add item (category browser or search → pick → quantity; browse.go) ---

// handleAddItem opens the item picker for a contract: the console message
// becomes the category browser (a Search button on it covers type-first users).
func (h *Feature) handleAddItem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return h.renderBrowseCategories(ctx, r, i, serverID, pickContractItem, cid)
}

func (h *Feature) submitAddItem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	query := strings.TrimSpace(modalTextValue(i.ModalSubmitData(), inQuery))
	if query == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_item", nil))
	}
	return h.runPick(ctx, r, i, serverID, pickContractItem, cid, query)
}

// --- item edit (quantity; the name is free-text-only — a gamedata item's name
// is catalog-owned) ---

func (h *Feature) openItemEditModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	prog, err := h.repo.ProgressByItemScoped(ctx, serverID, itemID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	name, qty, gd := "", "", false
	for _, it := range prog.Items {
		if it.ID == itemID {
			name, qty, gd = it.Name, strconv.Itoa(it.RequiredQty), it.GDID != ""
			break
		}
	}
	var comps []discordgo.MessageComponent
	if !gd {
		comps = append(comps, h.labelInput(ctx, serverID, "contracts.console.lbl_item_name", inName, discordgo.TextInputShort, name, true, 100))
	}
	comps = append(comps, h.labelInput(ctx, serverID, "contracts.console.lbl_qty", inQty, discordgo.TextInputShort, qty, true, 12))
	return r.RespondModal(i.Interaction, buildID(segMIEdit, itemID.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_item_edit_title"), comps)
}

func (h *Feature) submitItemEdit(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	// A gamedata item's modal has no name field; keep its stored snapshot (the
	// public panel's identity) and only change the quantity.
	prog, err := h.repo.ProgressByItemScoped(ctx, serverID, itemID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	current := ""
	for _, it := range prog.Items {
		if it.ID == itemID {
			if it.GDID != "" {
				current = it.Name
			}
			break
		}
	}
	data := i.ModalSubmitData()
	name := current
	if name == "" {
		name = normalizeItem(modalTextValue(data, inName))
	}
	qty, qerr := parseQty(modalTextValue(data, inQty))
	if name == "" || qerr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_item", nil))
	}
	if _, err := h.repo.UpdateItem(ctx, serverID, itemID, name, qty, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderItemView(ctx, r, i, serverID, itemID, 0, true)
}

// --- rewards + delivery location (contract view) ---

// rewardInputs builds the four reward fields, prefilled (shared with the
// template rewards modal). Four of Discord's five modal inputs — one slot left.
func (h *Feature) rewardInputs(ctx context.Context, serverID uuid.UUID, credits, reputation, licence, factor string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_credits", inCredits, discordgo.TextInputShort, credits, false, rewardFieldMaxLen),
		h.labelInput(ctx, serverID, "contracts.console.lbl_reputation", inReputation, discordgo.TextInputShort, reputation, false, 10),
		h.labelInput(ctx, serverID, "contracts.console.lbl_licence", inLicence, discordgo.TextInputShort, licence, false, 10),
		h.labelInput(ctx, serverID, "contracts.console.lbl_factor", inFactor, discordgo.TextInputShort, factor, false, factorFieldMaxLen),
	}
}

// rewardIntStr prefills an optional int reward field ("" for unset).
func rewardIntStr(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

// creditsStr prefills the credits field ("" for unset).
func creditsStr(d *decimal.Decimal) string {
	if d == nil {
		return ""
	}
	return d.String()
}

// factorStr prefills the participant-reward-factor field ("" for zero — a blank
// reads as "none" better than "0").
func factorStr(d decimal.Decimal) string {
	if d.IsZero() {
		return ""
	}
	return d.String()
}

func (h *Feature) openContractRewardsModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	prog, err := h.repo.ProgressByIDScoped(ctx, serverID, cid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	comps := h.rewardInputs(ctx, serverID, creditsStr(prog.RewardCredits), rewardIntStr(prog.RewardReputation), rewardIntStr(prog.RewardLicencePoints), factorStr(prog.ParticipantRewardFactor))
	return r.RespondModal(i.Interaction, buildID(segMCRew, cid.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_rewards_title"), comps)
}

func (h *Feature) submitContractRewards(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	data := i.ModalSubmitData()
	credits, cerr := parseCredits(modalTextValue(data, inCredits))
	reputation, rerr := parseRewardInt(modalTextValue(data, inReputation))
	licence, lerr := parseRewardInt(modalTextValue(data, inLicence))
	factor, ferr := parseFactor(modalTextValue(data, inFactor))
	if cerr != nil || rerr != nil || lerr != nil || ferr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_reward", nil))
	}
	if err := h.repo.UpdateRewards(ctx, serverID, cid, credits, factor, reputation, licence, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
}

// handleContractLocation opens the delivery-location picker: every space object
// fits one select, so the flow is modal-free (browse.go).
func (h *Feature) handleContractLocation(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	cid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return h.renderLocationBrowser(ctx, r, i, serverID, pickContractLoc, cid)
}

// --- link a legacy item to gamedata (search → picker) ---

func (h *Feature) openLinkItemModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	// Prefill the search with the item's stored free-text name — usually already
	// close to the catalog name.
	prog, err := h.repo.ProgressByItemScoped(ctx, serverID, itemID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	query := ""
	for _, it := range prog.Items {
		if it.ID == itemID {
			query = it.Name
			break
		}
	}
	comps := []discordgo.MessageComponent{
		h.searchInput(ctx, serverID, "contracts.console.search_hint", truncate(query, 100), true),
	}
	return r.RespondModal(i.Interaction, buildID(segMILink, itemID.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_link_title"), comps)
}

func (h *Feature) submitLinkItem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrItemNotFound)
	}
	query := strings.TrimSpace(modalTextValue(i.ModalSubmitData(), inQuery))
	if query == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_item", nil))
	}
	return h.runPick(ctx, r, i, serverID, pickItemLink, itemID, query)
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
