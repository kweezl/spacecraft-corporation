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

// reportsCustomID is this section's component id, under the "settings:" namespace
// so the registry routes it to the settings panel, which dispatches it here.
const reportsCustomID = "settings:reports"

// reportsSection contributes the contracts reports-channel control to the
// /settings panel (only when the contracts feature is enabled). The value lives
// in the settings store (ReportsConfig); the UI belongs to this feature, so it is
// added via the settings_sections fx group.
type reportsSection struct {
	reports ReportsConfig
	loc     *i18n.Localizer
}

// newReportsSection builds the section, returned as a settings.Section for the
// settings_sections fx group.
func newReportsSection(reports ReportsConfig, loc *i18n.Localizer) settings.Section {
	return &reportsSection{reports: reports, loc: loc}
}

// Rows renders a label and a text-channel select, prefilled with the currently
// configured reports channel (if any).
func (s *reportsSection) Rows(ctx context.Context, serverID uuid.UUID) []discordgo.MessageComponent {
	sel := discordgo.SelectMenu{
		MenuType:     discordgo.ChannelSelectMenu,
		CustomID:     reportsCustomID,
		Placeholder:  s.loc.Render(ctx, serverID, "settings.section.reports.placeholder", nil),
		MinValues:    intPtr(1),
		MaxValues:    1,
		ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews},
	}
	if ch, ok := s.reports.ContractsReportsChannelID(ctx, serverID); ok {
		sel.DefaultValues = []discordgo.SelectMenuDefaultValue{
			{ID: ch, Type: discordgo.SelectMenuDefaultValueChannel},
		}
	}
	return []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: s.loc.Render(ctx, serverID, "settings.section.reports.label", nil)},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{sel}},
	}
}

// Owns claims this section's CustomID.
func (s *reportsSection) Owns(customID string) bool { return customID == reportsCustomID }

// Handle persists the chosen reports channel and re-renders the panel. The
// settings panel has already re-authorized the settings gate before dispatching.
func (s *reportsSection) Handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, rerender func() []discordgo.MessageComponent) error {
	if vals := i.MessageComponentData().Values; len(vals) > 0 && vals[0] != "" {
		if err := s.reports.SetContractsReportsChannelID(ctx, serverID, vals[0]); err != nil {
			return fmt.Errorf("contracts: set reports channel: %w", err)
		}
	}
	return r.UpdateComponentsV2(i.Interaction, rerender())
}
