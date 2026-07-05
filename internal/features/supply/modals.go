package supply

import (
	"context"
	"errors"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// Modal input CustomIDs and field caps.
const (
	inTitle      = "title"
	inDesc       = "description"
	inConfirm    = "confirm"
	inQty        = "qty"
	inSystemName = "system_name"
	inSystemCode = "system_code"
	inPlanet     = "planet"
	inRefLink    = "ref_link"
	inQuery      = "query"

	titleMaxLen  = 100
	descMaxLen   = 2000
	systemMaxLen = 100
	planetMaxLen = 4
	refLinkMax   = 200
	qtyMaxLen    = 12
)

// labelInput builds a Label-wrapped text input.
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

// searchInput is the gamedata search field: a Label-wrapped text input whose
// description spells out the search-then-pick flow (modals have no live
// autocomplete, so matches appear only after submit).
func (h *Feature) searchInput(ctx context.Context, serverID uuid.UUID, value string) discordgo.Label {
	return discordgo.Label{
		Label:       h.loc.Render(ctx, serverID, "supply.console.lbl_search", nil),
		Description: truncate(h.loc.Render(ctx, serverID, "supply.console.search_hint", nil), 100),
		Component: discordgo.TextInput{
			CustomID:    inQuery,
			Style:       discordgo.TextInputShort,
			Value:       value,
			Placeholder: truncate(h.loc.Render(ctx, serverID, "supply.console.search_placeholder", nil), 100),
			Required:    boolPtr(true),
			MaxLength:   100,
		},
	}
}

// modalTitle renders a modal title clamped to Discord's limit.
func (h *Feature) modalTitle(ctx context.Context, serverID uuid.UUID, key string) string {
	return truncate(h.loc.Render(ctx, serverID, key, nil), modalTitleMax)
}

// --- create ---

func (h *Feature) openCreateModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "supply.console.lbl_title", inTitle, discordgo.TextInputShort, "", true, titleMaxLen),
		h.labelInput(ctx, serverID, "supply.console.lbl_description", inDesc, discordgo.TextInputParagraph, "", false, descMaxLen),
	}
	return r.RespondModal(i.Interaction, buildID(segMNew), h.modalTitle(ctx, serverID, "supply.console.modal_create_title"), comps)
}

func (h *Feature) submitCreate(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	data := i.ModalSubmitData()
	title := strings.TrimSpace(modalTextValue(data, inTitle))
	if title == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.console.bad_name", nil))
	}
	if _, ok := h.forum.SupplyForumChannelID(ctx, serverID); !ok {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.console.no_forum", nil))
	}
	limit := h.requestLimit(ctx, serverID)
	rid, err := h.repo.Create(ctx, CreateInput{
		ServerID:    serverID,
		OwnerUserID: invokerID(i),
		Title:       title,
		Description: strings.TrimSpace(modalTextValue(data, inDesc)),
		OpenLimit:   limit,
	})
	if errors.Is(err, ErrLimit) {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.console.limit_reached", map[string]any{"Limit": limit}))
	}
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderRequestView(ctx, r, i, serverID, rid, 0, true)
}

// --- edit details (title + description) ---

func (h *Feature) openEditModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	prog, err := h.repo.ProgressByIDOwned(ctx, serverID, invokerID(i), rid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "supply.console.lbl_title", inTitle, discordgo.TextInputShort, prog.Title, true, titleMaxLen),
		h.labelInput(ctx, serverID, "supply.console.lbl_description", inDesc, discordgo.TextInputParagraph, prog.Description, false, descMaxLen),
	}
	return r.RespondModal(i.Interaction, buildID(segMREdit, rid.String()), h.modalTitle(ctx, serverID, "supply.console.modal_edit_title"), comps)
}

func (h *Feature) submitEdit(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	data := i.ModalSubmitData()
	title := strings.TrimSpace(modalTextValue(data, inTitle))
	if title == "" {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.console.bad_name", nil))
	}
	if err := h.repo.UpdateDetails(ctx, serverID, invokerID(i), rid, title, strings.TrimSpace(modalTextValue(data, inDesc))); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderRequestView(ctx, r, i, serverID, rid, 0, true)
}

// --- close (typed confirm: type the request title) ---

func (h *Feature) openCloseModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "supply.console.lbl_close_confirm", inConfirm, discordgo.TextInputShort, "", true, titleMaxLen),
	}
	return r.RespondModal(i.Interaction, buildID(segMRClose, rid.String()), h.modalTitle(ctx, serverID, "supply.console.modal_close_title"), comps)
}

func (h *Feature) submitClose(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	prog, err := h.repo.ProgressByIDOwned(ctx, serverID, invokerID(i), rid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	typed := strings.TrimSpace(modalTextValue(i.ModalSubmitData(), inConfirm))
	if !strings.EqualFold(typed, strings.TrimSpace(prog.Title)) {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.console.close_mismatch", nil))
	}
	if err := h.repo.Cancel(ctx, serverID, invokerID(i), rid); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderRequestView(ctx, r, i, serverID, rid, 0, true)
}

// --- system (name / code / planet) ---

func (h *Feature) openSystemModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	prog, err := h.repo.ProgressByIDOwned(ctx, serverID, invokerID(i), rid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	planet := ""
	if prog.PlanetNumber != nil {
		planet = intStr(*prog.PlanetNumber)
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "supply.console.lbl_system_name", inSystemName, discordgo.TextInputShort, prog.SystemName, false, systemMaxLen),
		h.labelInput(ctx, serverID, "supply.console.lbl_system_code", inSystemCode, discordgo.TextInputShort, prog.SystemCode, false, systemMaxLen),
		h.labelInput(ctx, serverID, "supply.console.lbl_planet", inPlanet, discordgo.TextInputShort, planet, false, planetMaxLen),
	}
	return r.RespondModal(i.Interaction, buildID(segMRSys, rid.String()), h.modalTitle(ctx, serverID, "supply.console.modal_system_title"), comps)
}

func (h *Feature) submitSystem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	data := i.ModalSubmitData()
	planet, ok := parsePlanet(modalTextValue(data, inPlanet))
	if !ok {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.console.bad_planet", nil))
	}
	if err := h.repo.SetSystemInfo(ctx, serverID, invokerID(i), rid,
		strings.TrimSpace(modalTextValue(data, inSystemName)),
		strings.TrimSpace(modalTextValue(data, inSystemCode)), planet); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderRequestView(ctx, r, i, serverID, rid, 0, true)
}

// --- reference message link ---

func (h *Feature) openRefModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	prog, err := h.repo.ProgressByIDOwned(ctx, serverID, invokerID(i), rid)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	current := ""
	if prog.RefMessage != nil {
		current = prog.RefMessage.Link()
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "supply.console.lbl_ref_link", inRefLink, discordgo.TextInputShort, current, false, refLinkMax),
	}
	return r.RespondModal(i.Interaction, buildID(segMRRef, rid.String()), h.modalTitle(ctx, serverID, "supply.console.modal_ref_title"), comps)
}

func (h *Feature) submitRef(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	link := strings.TrimSpace(modalTextValue(i.ModalSubmitData(), inRefLink))
	if link == "" {
		// Empty input clears the reference.
		if err := h.repo.SetMessageRef(ctx, serverID, invokerID(i), rid, "", "", ""); err != nil {
			return h.consoleErr(ctx, r, i, serverID, err)
		}
		return h.renderRequestView(ctx, r, i, serverID, rid, 0, true)
	}
	ref, valid := parseMessageRef(link, i.GuildID)
	if !valid {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.console.bad_link", nil))
	}
	if err := h.repo.SetMessageRef(ctx, serverID, invokerID(i), rid, ref.GuildID, ref.ChannelID, ref.MessageID); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderRequestView(ctx, r, i, serverID, rid, 0, true)
}

// --- item quantity edit ---

func (h *Feature) openItemQtyModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "supply.console.lbl_qty", inQty, discordgo.TextInputShort, "", true, qtyMaxLen),
	}
	return r.RespondModal(i.Interaction, buildID(segMIEdit, itemID.String()), h.modalTitle(ctx, serverID, "supply.console.modal_qty_title"), comps)
}

func (h *Feature) submitItemQty(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	qty, err := parseQty(modalTextValue(i.ModalSubmitData(), inQty))
	if err != nil {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.console.bad_qty", nil))
	}
	if err := h.repo.UpdateItemQty(ctx, serverID, invokerID(i), itemID, qty); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	// Re-render the owning request view.
	prog, err := h.repo.ProgressByItemOwned(ctx, serverID, invokerID(i), itemID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderRequestView(ctx, r, i, serverID, prog.ID, 0, true)
}

// --- item removal (confirm) ---

func (h *Feature) openRemoveItemModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	comps := []discordgo.MessageComponent{
		h.labelInput(ctx, serverID, "supply.console.lbl_remove_confirm", inConfirm, discordgo.TextInputShort, "", true, titleMaxLen),
	}
	return r.RespondModal(i.Interaction, buildID(segMIDel, itemID.String()), h.modalTitle(ctx, serverID, "supply.console.modal_remove_title"), comps)
}

func (h *Feature) submitRemoveItem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	itemID, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	// Resolve the owning request before deletion so we can re-render it after.
	prog, err := h.repo.ProgressByItemOwned(ctx, serverID, invokerID(i), itemID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	if _, err := h.repo.RemoveItem(ctx, serverID, invokerID(i), itemID); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderRequestView(ctx, r, i, serverID, prog.ID, 0, true)
}

// --- browser / location / republish entry points ---

// handleOpenAddItem opens the item browser for the request (dest si).
func (h *Feature) handleOpenAddItem(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return h.pick.RenderBrowse(ctx, r, i, serverID, destItem, rid)
}

// submitAddItemSearch runs the item search (dest si) when the search modal opened
// from the browser is submitted.
func (h *Feature) submitAddItemSearch(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return h.pick.RunPick(ctx, r, i, serverID, destItem, rid, strings.TrimSpace(modalTextValue(i.ModalSubmitData(), inQuery)))
}

// handleOpenLocation opens the delivery-location browser for the request (dest sl).
func (h *Feature) handleOpenLocation(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	return h.pick.RenderLocation(ctx, r, i, serverID, destLoc, rid)
}

// handleRepublish re-posts the request's forum thread.
func (h *Feature) handleRepublish(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, parts []string) error {
	rid, ok := argUUID(parts, 0)
	if !ok {
		return h.consoleErr(ctx, r, i, serverID, ErrNotFound)
	}
	if err := h.repo.Republish(ctx, serverID, invokerID(i), rid); err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}
	return h.renderRequestView(ctx, r, i, serverID, rid, 0, true)
}
