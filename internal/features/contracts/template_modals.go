package contracts

import (
	"context"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
)

// The template library's modal flows. Discord caps a modal at 5 inputs and
// forbids opening a modal from a modal submit, which fixes the shape of the
// pages: create asks only title + description and lands on the edit page, where
// details (title + description + D/H/M = 5), rewards (3), location (search →
// picker), and items (search + qty → picker) are each their own modal.

// --- search (list filter) ---

func (h *Feature) openTemplateSearchModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	mode := tplModePick
	if len(parts) > 0 && parts[0] == tplModeManage {
		mode = tplModeManage
	}
	comps := []discordgo.MessageComponent{
		h.searchInput(ctx, serverID, "contracts.console.tpl_search_hint", "", false),
	}
	return r.RespondModal(i.Interaction, buildID(segMTSearch, mode), h.modalTitle(ctx, serverID, "contracts.console.modal_search_title"), comps)
}

func (h *Feature) submitTemplateSearch(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	mode := tplModePick
	if len(parts) > 0 && parts[0] == tplModeManage {
		mode = tplModeManage
	}
	query := strings.TrimSpace(modalTextValue(i.ModalSubmitData(), inQuery))
	return h.renderTemplatesView(ctx, r, i, serverID, mode, 0, query, true)
}

// --- create (title + description → the edit page) ---

func (h *Feature) openTemplateNewModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_name", inName, discordgo.TextInputShort, "", true, titleMaxLen),
		h.labelInput(ctx, serverID, "contracts.console.lbl_description", inDesc, discordgo.TextInputParagraph, "", false, descriptionMaxLen),
	}
	return r.RespondModal(i.Interaction, buildID(segMTNew), h.modalTitle(ctx, serverID, "contracts.console.modal_tpl_new_title"), comps)
}

func (h *Feature) submitTemplateNew(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	data := i.ModalSubmitData()
	title := normalizeItem(modalTextValue(data, inName))
	if title == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_name", nil))
	}
	tid, err := h.tpls.CreateTemplate(ctx, serverID, title, strings.TrimSpace(modalTextValue(data, inDesc)), invokerID(i))
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	// Straight onto the edit page: rewards/duration/location/items are edited
	// there (a modal may not open another modal).
	return h.renderTemplateEditView(ctx, r, i, serverID, tid, 0, true)
}

// --- details (title + description + default duration) ---

func (h *Feature) openTemplateDetailsModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	t, err := h.tpls.TemplateByID(ctx, serverID, tid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	d, hh, m := dhmStrings(t.DeadlineMinutes)
	comps := append([]discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_name", inName, discordgo.TextInputShort, t.Title, true, titleMaxLen),
		h.labelInput(ctx, serverID, "contracts.console.lbl_description", inDesc, discordgo.TextInputParagraph, t.Description, false, descriptionMaxLen),
	}, h.dhmInputs(ctx, serverID, d, hh, m)...)
	return r.RespondModal(i.Interaction, buildID(segMTEdit, tid.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_tpl_edit_title"), comps)
}

func (h *Feature) submitTemplateDetails(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	data := i.ModalSubmitData()
	title := normalizeItem(modalTextValue(data, inName))
	if title == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_name", nil))
	}
	minutes, derr := parseDHMMinutes(modalTextValue(data, inDays), modalTextValue(data, inHours), modalTextValue(data, inMinutes))
	if derr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_deadline", nil))
	}
	if err := h.tpls.UpdateTemplateDetails(ctx, serverID, tid, title, strings.TrimSpace(modalTextValue(data, inDesc)), minutes, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderTemplateEditView(ctx, r, i, serverID, tid, 0, true)
}

// --- rewards ---

func (h *Feature) openTemplateRewardsModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	t, err := h.tpls.TemplateByID(ctx, serverID, tid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	credits := ""
	if t.RewardCredits.IsPositive() {
		credits = t.RewardCredits.String()
	}
	rep, lic := "", ""
	if t.RewardReputation > 0 {
		rep = intStr(t.RewardReputation)
	}
	if t.RewardLicencePoints > 0 {
		lic = intStr(t.RewardLicencePoints)
	}
	comps := h.rewardInputs(ctx, serverID, credits, rep, lic)
	return r.RespondModal(i.Interaction, buildID(segMTRew, tid.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_rewards_title"), comps)
}

func (h *Feature) submitTemplateRewards(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	data := i.ModalSubmitData()
	credits, cerr := parseCredits(modalTextValue(data, inCredits))
	reputation, rerr := parseRewardInt(modalTextValue(data, inReputation))
	licence, lerr := parseRewardInt(modalTextValue(data, inLicence))
	if cerr != nil || rerr != nil || lerr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_reward", nil))
	}
	// Template rewards are NOT NULL: blank fields mean zero, not unset.
	cr := decimal.Zero
	if credits != nil {
		cr = *credits
	}
	rep, lic := 0, 0
	if reputation != nil {
		rep = *reputation
	}
	if licence != nil {
		lic = *licence
	}
	if err := h.tpls.UpdateTemplateRewards(ctx, serverID, tid, cr, rep, lic, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderTemplateEditView(ctx, r, i, serverID, tid, 0, true)
}

// --- delivery location (modal-free picker; browse.go) ---

// handleTemplateLocation opens the delivery-location picker for a template.
func (h *Feature) handleTemplateLocation(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	return h.renderLocationBrowser(ctx, r, i, serverID, pickTemplateLoc, tid)
}

// --- items (category browser or search → pick → quantity; qty edit) ---

// handleTemplateAddItem opens the item picker for a template: the console
// message becomes the category browser (with a Search button for type-first
// users).
func (h *Feature) handleTemplateAddItem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	return h.renderBrowseCategories(ctx, r, i, serverID, pickTemplateItem, tid)
}

func (h *Feature) submitTemplateAddItem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	query := strings.TrimSpace(modalTextValue(i.ModalSubmitData(), inQuery))
	if query == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_item", nil))
	}
	return h.runPick(ctx, r, i, serverID, pickTemplateItem, tid, query)
}

func (h *Feature) openTemplateItemQtyModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateItemNotFound)
	}
	// The current qty rides the opening button's CustomID (parts[1]) — no by-item
	// read exists, and the prefill is display-only anyway.
	qty := ""
	if n := argInt(parts, 1); n > 0 {
		qty = intStr(n)
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_qty", inQty, discordgo.TextInputShort, qty, true, 12),
	}
	return r.RespondModal(i.Interaction, buildID(segMTIEdit, itemID.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_item_edit_title"), comps)
}

func (h *Feature) submitTemplateItemQty(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateItemNotFound)
	}
	qty, qerr := parseQty(modalTextValue(i.ModalSubmitData(), inQty))
	if qerr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_item", nil))
	}
	tid, err := h.tpls.UpdateTemplateItemQty(ctx, serverID, itemID, qty, invokerID(i))
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderTemplateEditView(ctx, r, i, serverID, tid, 0, true)
}

// --- delete (typed confirmation) ---

func (h *Feature) openTemplateDeleteModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	return r.RespondModal(i.Interaction, buildID(segMTDel, tid.String()),
		h.modalTitle(ctx, serverID, "contracts.console.modal_tpl_delete_title"),
		[]discordgo.MessageComponent{h.confirmInput(ctx, serverID)})
}

func (h *Feature) submitTemplateDelete(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	if !h.confirmed(ctx, serverID, i.ModalSubmitData()) {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.confirm_mismatch", nil))
	}
	if err := h.tpls.DeleteTemplate(ctx, serverID, tid, invokerID(i)); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderTemplatesView(ctx, r, i, serverID, tplModeManage, 0, "", true)
}

// --- instantiate ("Use": confirm title + deadline, then create the contract) ---

func (h *Feature) openUseTemplateModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	t, err := h.tpls.TemplateByID(ctx, serverID, tid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	// Title and the deadline duration prefill from the template — everything is a
	// default the creator may override before the contract is posted.
	d, hh, m := dhmStrings(t.DeadlineMinutes)
	comps := append([]discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "contracts.console.lbl_name", inName, discordgo.TextInputShort, t.Title, true, titleMaxLen),
	}, h.dhmInputs(ctx, serverID, d, hh, m)...)
	return r.RespondModal(i.Interaction, buildID(segMTUse, tid.String()), h.modalTitle(ctx, serverID, "contracts.console.modal_tpl_use_title"), comps)
}

func (h *Feature) submitUseTemplate(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	tid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrTemplateNotFound)
	}
	data := i.ModalSubmitData()
	title := normalizeItem(modalTextValue(data, inName))
	if title == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_name", nil))
	}
	deadline, derr := parseDHM(modalTextValue(data, inDays), modalTextValue(data, inHours), modalTextValue(data, inMinutes))
	if derr != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.bad_deadline", nil))
	}
	if _, ok := h.forum.ContractsForumChannelID(ctx, serverID); !ok {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.no_forum", nil))
	}
	t, err := h.tpls.TemplateByID(ctx, serverID, tid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}

	// Copy the template's values BY VALUE: later template edits or deletion must
	// never change this contract. Item versions carry over from the template rows
	// (provenance); name snapshots resolve through each item's own stamped catalog.
	in := CreateInput{
		ServerID:          serverID,
		Kind:              KindTemplate,
		TemplateID:        &tid,
		Title:             title,
		Description:       t.Description,
		Deadline:          deadline,
		LocationGDID:      t.LocationGDID,
		LocationGDVersion: t.LocationGDVersion,
		CreatedByUserID:   invokerID(i),
		// No AppID/Token: the console navigates to the new contract itself, so the
		// worker must NOT edit this interaction's response (see submitCreate).
	}
	if t.RewardCredits.IsPositive() {
		cr := t.RewardCredits
		in.RewardCredits = &cr
	}
	if t.RewardReputation > 0 {
		rep := t.RewardReputation
		in.RewardReputation = &rep
	}
	if t.RewardLicencePoints > 0 {
		lic := t.RewardLicencePoints
		in.RewardLicencePoints = &lic
	}
	lang := h.lang(ctx, serverID)
	for _, it := range t.Items {
		name := it.GDID
		if cat := h.catalogFor(it.GDVersion); cat != nil {
			if n := cat.Name(gamedata.GDID(it.GDID), lang); n != "" {
				name = n
			}
		}
		in.Items = append(in.Items, CreateItemInput{Name: name, GDID: it.GDID, GDVersion: it.GDVersion, Qty: it.Qty})
	}

	cid, err := h.repo.Create(ctx, in)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderContractView(ctx, r, i, serverID, cid, 0, true)
}
