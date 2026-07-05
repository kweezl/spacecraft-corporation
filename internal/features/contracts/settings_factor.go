package contracts

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/settings"
)

// The factor section's component ids, under the "settings:" namespace like the
// forum section: the edit button and the modal it opens (modal submits route
// back through the settings panel to this section).
const (
	factorCustomID      = "settings:contracts_factor"
	factorModalCustomID = "settings:contracts_factor_modal"
)

// factorSection contributes the default participant-reward-factor control to
// the /settings panel (only when the contracts feature is enabled). The value
// lives in the settings store (RewardDefaults); a free-form decimal needs a
// text input, so unlike the forum section's channel select this one opens a
// one-field modal.
type factorSection struct {
	defaults RewardDefaults
	loc      *i18n.Localizer
}

// newFactorSection builds the section, returned as a settings.Section for the
// settings_sections fx group.
func newFactorSection(defaults RewardDefaults, loc *i18n.Localizer) settings.Section {
	return &factorSection{defaults: defaults, loc: loc}
}

// Rows renders the current default factor and an edit button.
func (s *factorSection) Rows(ctx context.Context, serverID uuid.UUID) []discordgo.MessageComponent {
	factor := s.defaults.ContractsRewardFactor(ctx, serverID)
	return []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: s.loc.Render(ctx, serverID, "settings.section.factor.label",
			map[string]any{"Factor": factor.String()})},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    s.loc.Render(ctx, serverID, "settings.section.factor.button", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: factorCustomID,
			},
		}},
	}
}

// Owns claims the edit button and its modal.
func (s *factorSection) Owns(customID string) bool {
	return customID == factorCustomID || customID == factorModalCustomID
}

// Handle drives the two-step edit: the button opens a one-field modal prefilled
// with the current factor; the modal submit validates (0–100, up to two
// decimals; blank = 0), persists, and re-renders the panel. Invalid input gets
// its own ephemeral reply, leaving the panel untouched. The settings panel has
// already re-authorized the settings gate before dispatching here.
func (s *factorSection) Handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, rerender func() []discordgo.MessageComponent) error {
	if i.Type != discordgo.InteractionModalSubmit {
		factor := s.defaults.ContractsRewardFactor(ctx, serverID)
		input := discordgo.Label{
			Label: s.loc.Render(ctx, serverID, "contracts.console.lbl_factor", nil),
			Component: discordgo.TextInput{
				CustomID:  inFactor,
				Style:     discordgo.TextInputShort,
				Value:     factorStr(factor),
				Required:  boolPtr(false),
				MaxLength: factorFieldMaxLen,
			},
		}
		title := truncate(s.loc.Render(ctx, serverID, "settings.section.factor.modal_title", nil), modalTitleMax)
		return r.RespondModal(i.Interaction, factorModalCustomID, title, []discordgo.MessageComponent{input})
	}

	factor, err := parseFactor(modalTextValue(i.ModalSubmitData(), inFactor))
	if err != nil {
		return r.RespondEphemeral(i.Interaction, s.loc.Render(ctx, serverID, "settings.section.factor.bad_value", nil))
	}
	if err := s.defaults.SetContractsRewardFactor(ctx, serverID, factor); err != nil {
		return fmt.Errorf("contracts: set reward factor: %w", err)
	}
	return r.UpdateComponentsV2(i.Interaction, rerender())
}
