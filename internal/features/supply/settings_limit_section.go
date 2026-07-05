package supply

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

// The limit section's component ids, under the "settings:" namespace.
const (
	supplyLimitCustomID      = "settings:supply_limit"
	supplyLimitModalCustomID = "settings:supply_limit_modal"
	supplyLimitFieldMaxLen   = 3 // up to 999
	inSupplyLimit            = "limit"
	// supplyLimitMax bounds the per-member open-request cap a server may set.
	supplyLimitMax = 100
)

// limitSettings resolves and sets the supply per-member open-request limit.
// Implemented by settings.Store.
type limitSettings interface {
	SupplyRequestLimit(ctx context.Context, serverID uuid.UUID) (int, bool)
	SetSupplyRequestLimit(ctx context.Context, serverID uuid.UUID, limit int) error
}

// limitSection contributes the per-member open-request-limit control to the
// /settings panel. A free-form integer needs a text input, so like the contracts
// factor section it opens a one-field modal.
type limitSection struct {
	limit limitSettings
	loc   *i18n.Localizer
}

func newLimitSection(limit limitSettings, loc *i18n.Localizer) settings.Section {
	return &limitSection{limit: limit, loc: loc}
}

// current resolves the effective limit for display/prefill.
func (s *limitSection) current(ctx context.Context, serverID uuid.UUID) int {
	if v, ok := s.limit.SupplyRequestLimit(ctx, serverID); ok {
		return v
	}
	return DefaultRequestLimit
}

func (s *limitSection) Rows(ctx context.Context, serverID uuid.UUID) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: s.loc.Render(ctx, serverID, "settings.section.supply_limit.label",
			map[string]any{"Limit": s.current(ctx, serverID)})},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    s.loc.Render(ctx, serverID, "settings.section.supply_limit.button", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: supplyLimitCustomID,
			},
		}},
	}
}

func (s *limitSection) Owns(customID string) bool {
	return customID == supplyLimitCustomID || customID == supplyLimitModalCustomID
}

func (s *limitSection) Handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, rerender func() []discordgo.MessageComponent) error {
	if i.Type != discordgo.InteractionModalSubmit {
		input := discordgo.Label{
			Label: s.loc.Render(ctx, serverID, "settings.section.supply_limit.field_label", nil),
			Component: discordgo.TextInput{
				CustomID:  inSupplyLimit,
				Style:     discordgo.TextInputShort,
				Value:     strconv.Itoa(s.current(ctx, serverID)),
				Required:  boolPtr(true),
				MaxLength: supplyLimitFieldMaxLen,
			},
		}
		title := truncate(s.loc.Render(ctx, serverID, "settings.section.supply_limit.modal_title", nil), modalTitleMax)
		return r.RespondModal(i.Interaction, supplyLimitModalCustomID, title, []discordgo.MessageComponent{input})
	}

	n, err := strconv.Atoi(modalTextValue(i.ModalSubmitData(), inSupplyLimit))
	if err != nil || n < 1 || n > supplyLimitMax {
		return r.RespondEphemeral(i.Interaction, s.loc.Render(ctx, serverID, "settings.section.supply_limit.bad_value",
			map[string]any{"Max": supplyLimitMax}))
	}
	if err := s.limit.SetSupplyRequestLimit(ctx, serverID, n); err != nil {
		return fmt.Errorf("supply: set request limit: %w", err)
	}
	return r.UpdateComponentsV2(i.Interaction, rerender())
}
