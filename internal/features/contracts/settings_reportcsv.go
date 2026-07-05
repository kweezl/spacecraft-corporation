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

// reportCSVCustomID is this section's component id, under the "settings:"
// namespace so the registry routes it to the settings panel, which dispatches it
// here.
const reportCSVCustomID = "settings:contracts_report_csv"

// The two toggle values carried by the select's options.
const (
	reportCSVOn  = "on"
	reportCSVOff = "off"
)

// reportCSVSection contributes the payout-CSV attachment toggle to the /settings
// panel (only when the contracts feature is enabled). The value lives in the
// settings store (ReportCSVConfig); the UI belongs to this feature, so it is
// added via the settings_sections fx group. A boolean setting needs no free-form
// input, so it renders a two-option select (on/off) rather than a modal.
type reportCSVSection struct {
	cfg ReportCSVConfig
	loc *i18n.Localizer
}

// newReportCSVSection builds the section, returned as a settings.Section for the
// settings_sections fx group.
func newReportCSVSection(cfg ReportCSVConfig, loc *i18n.Localizer) settings.Section {
	return &reportCSVSection{cfg: cfg, loc: loc}
}

// Rows renders a label and a two-option select, with the current state marked as
// the default option.
func (s *reportCSVSection) Rows(ctx context.Context, serverID uuid.UUID) []discordgo.MessageComponent {
	enabled := s.cfg.ContractsReportCSV(ctx, serverID)
	sel := discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    reportCSVCustomID,
		Placeholder: s.loc.Render(ctx, serverID, "settings.section.contracts_report_csv.placeholder", nil),
		MinValues:   intPtr(1),
		MaxValues:   1,
		Options: []discordgo.SelectMenuOption{
			{
				Label:   s.loc.Render(ctx, serverID, "settings.section.contracts_report_csv.enabled", nil),
				Value:   reportCSVOn,
				Default: enabled,
			},
			{
				Label:   s.loc.Render(ctx, serverID, "settings.section.contracts_report_csv.disabled", nil),
				Value:   reportCSVOff,
				Default: !enabled,
			},
		},
	}
	return []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: s.loc.Render(ctx, serverID, "settings.section.contracts_report_csv.label", nil)},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{sel}},
	}
}

// Owns claims this section's CustomID.
func (s *reportCSVSection) Owns(customID string) bool { return customID == reportCSVCustomID }

// Handle persists the chosen toggle and re-renders the panel. The settings panel
// has already re-authorized the settings gate before dispatching.
func (s *reportCSVSection) Handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, rerender func() []discordgo.MessageComponent) error {
	if vals := i.MessageComponentData().Values; len(vals) > 0 {
		if err := s.cfg.SetContractsReportCSV(ctx, serverID, vals[0] == reportCSVOn); err != nil {
			return fmt.Errorf("contracts: set report csv: %w", err)
		}
	}
	return r.UpdateComponentsV2(i.Interaction, rerender())
}
