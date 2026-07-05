package contracts

import (
	"context"
	"fmt"
	"strconv"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/settings"
)

// The max-items section's component ids, under the "settings:" namespace like the
// other sections: the edit button and the modal it opens.
const (
	maxItemsCustomID      = "settings:contracts_max_items"
	maxItemsModalCustomID = "settings:contracts_max_items_modal"
	maxItemsFieldMaxLen   = 4 // up to 9999 distinct items
	inMaxItems            = "max_items"
)

// maxItemsSection contributes the per-contract distinct-item cap control to the
// /settings panel (only when the contracts feature is enabled). The value lives
// in the settings store (ItemCap); it replaces the former CONTRACTS_MAX_ITEMS
// env var. A free-form integer needs a text input, so like the factor section it
// opens a one-field modal.
type maxItemsSection struct {
	itemCap ItemCap
	loc     *i18n.Localizer
}

// newMaxItemsSection builds the section, returned as a settings.Section for the
// settings_sections fx group.
func newMaxItemsSection(itemCap ItemCap, loc *i18n.Localizer) settings.Section {
	return &maxItemsSection{itemCap: itemCap, loc: loc}
}

// current resolves the effective cap for display/prefill (the stored value, or
// DefaultMaxItems when unset).
func (s *maxItemsSection) current(ctx context.Context, serverID uuid.UUID) int {
	if v, ok := s.itemCap.ContractsMaxItems(ctx, serverID); ok {
		return v
	}
	return DefaultMaxItems
}

// Rows renders the current cap and an edit button.
func (s *maxItemsSection) Rows(ctx context.Context, serverID uuid.UUID) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: s.loc.Render(ctx, serverID, "settings.section.contracts_max_items.label",
			map[string]any{"Limit": s.current(ctx, serverID)})},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    s.loc.Render(ctx, serverID, "settings.section.contracts_max_items.button", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: maxItemsCustomID,
			},
		}},
	}
}

// Owns claims the edit button and its modal.
func (s *maxItemsSection) Owns(customID string) bool {
	return customID == maxItemsCustomID || customID == maxItemsModalCustomID
}

// Handle drives the two-step edit: the button opens a one-field modal prefilled
// with the current cap; the modal submit validates (a whole number ≥ 1),
// persists, and re-renders the panel. Invalid input gets its own ephemeral
// reply, leaving the panel untouched. The settings panel has already
// re-authorized the settings gate before dispatching here.
func (s *maxItemsSection) Handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, rerender func() []discordgo.MessageComponent) error {
	if i.Type != discordgo.InteractionModalSubmit {
		input := discordgo.Label{
			Label: s.loc.Render(ctx, serverID, "settings.section.contracts_max_items.field_label", nil),
			Component: discordgo.TextInput{
				CustomID:  inMaxItems,
				Style:     discordgo.TextInputShort,
				Value:     strconv.Itoa(s.current(ctx, serverID)),
				Required:  boolPtr(true),
				MaxLength: maxItemsFieldMaxLen,
			},
		}
		title := truncate(s.loc.Render(ctx, serverID, "settings.section.contracts_max_items.modal_title", nil), modalTitleMax)
		return r.RespondModal(i.Interaction, maxItemsModalCustomID, title, []discordgo.MessageComponent{input})
	}

	limit, err := parseQty(modalTextValue(i.ModalSubmitData(), inMaxItems))
	if err != nil {
		return r.RespondEphemeral(i.Interaction, s.loc.Render(ctx, serverID, "settings.section.contracts_max_items.bad_value", nil))
	}
	if err := s.itemCap.SetContractsMaxItems(ctx, serverID, limit); err != nil {
		return fmt.Errorf("contracts: set max items: %w", err)
	}
	return r.UpdateComponentsV2(i.Interaction, rerender())
}
