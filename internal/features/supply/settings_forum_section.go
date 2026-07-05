package supply

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/settings"
)

// supplyForumCustomID is the forum section's component id, under the "settings:"
// namespace so the settings panel routes it here via Owns.
const supplyForumCustomID = "settings:supply_forum"

// forumSettings resolves and sets the supply forum channel. Implemented by
// settings.Store (get + set, unlike the read-only ForumConfig used at runtime).
type forumSettings interface {
	SupplyForumChannelID(ctx context.Context, serverID uuid.UUID) (string, bool)
	SetSupplyForumChannelID(ctx context.Context, serverID uuid.UUID, channelID string) error
}

// forumSection contributes the supply forum-channel control to the /settings
// panel (only when the supply feature is enabled).
type forumSection struct {
	forum forumSettings
	loc   *i18n.Localizer
}

func newForumSection(forum forumSettings, loc *i18n.Localizer) settings.Section {
	return &forumSection{forum: forum, loc: loc}
}

func (s *forumSection) Rows(ctx context.Context, serverID uuid.UUID) []discordgo.MessageComponent {
	sel := discordgo.SelectMenu{
		MenuType:     discordgo.ChannelSelectMenu,
		CustomID:     supplyForumCustomID,
		Placeholder:  s.loc.Render(ctx, serverID, "settings.section.supply_forum.placeholder", nil),
		MinValues:    intPtr(1),
		MaxValues:    1,
		ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildForum},
	}
	if ch, ok := s.forum.SupplyForumChannelID(ctx, serverID); ok {
		sel.DefaultValues = []discordgo.SelectMenuDefaultValue{
			{ID: ch, Type: discordgo.SelectMenuDefaultValueChannel},
		}
	}
	return []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: s.loc.Render(ctx, serverID, "settings.section.supply_forum.label", nil)},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{sel}},
	}
}

func (s *forumSection) Owns(customID string) bool { return customID == supplyForumCustomID }

func (s *forumSection) Handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, rerender func() []discordgo.MessageComponent) error {
	if vals := i.MessageComponentData().Values; len(vals) > 0 && vals[0] != "" {
		if err := s.forum.SetSupplyForumChannelID(ctx, serverID, vals[0]); err != nil {
			return fmt.Errorf("supply: set forum channel: %w", err)
		}
	}
	return r.UpdateComponentsV2(i.Interaction, rerender())
}
