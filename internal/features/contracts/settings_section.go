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

// forumCustomID is this section's component id. It lives under the "settings:"
// namespace so the registry routes it to the settings panel, which dispatches it
// to this section via Owns.
const forumCustomID = "settings:forum"

// forumSection contributes the contracts forum-channel control to the /settings
// panel (only when the contracts feature is enabled). The value itself lives in
// the settings store (ForumConfig), but the UI belongs to this feature, so it is
// added via the settings_sections fx group rather than hardcoded in settings.
type forumSection struct {
	forum ForumConfig
	loc   *i18n.Localizer
}

// newForumSection builds the section, returned as a settings.Section for the
// settings_sections fx group.
func newForumSection(forum ForumConfig, loc *i18n.Localizer) settings.Section {
	return &forumSection{forum: forum, loc: loc}
}

// Rows renders a label and a forum-only channel select, prefilled with the
// currently configured channel (if any).
func (s *forumSection) Rows(ctx context.Context, serverID uuid.UUID) []discordgo.MessageComponent {
	sel := discordgo.SelectMenu{
		MenuType:     discordgo.ChannelSelectMenu,
		CustomID:     forumCustomID,
		Placeholder:  s.loc.Render(ctx, serverID, "settings.section.forum.placeholder", nil),
		MinValues:    intPtr(1),
		MaxValues:    1,
		ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildForum},
	}
	if ch, ok := s.forum.ContractsForumChannelID(ctx, serverID); ok {
		sel.DefaultValues = []discordgo.SelectMenuDefaultValue{
			{ID: ch, Type: discordgo.SelectMenuDefaultValueChannel},
		}
	}
	return []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: s.loc.Render(ctx, serverID, "settings.section.forum.label", nil)},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{sel}},
	}
}

// Owns claims this section's CustomID.
func (s *forumSection) Owns(customID string) bool { return customID == forumCustomID }

// Handle persists the chosen forum channel and re-renders the panel. The settings
// panel has already re-authorized the settings gate before dispatching here.
func (s *forumSection) Handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, rerender func() []discordgo.MessageComponent) error {
	if vals := i.MessageComponentData().Values; len(vals) > 0 && vals[0] != "" {
		if err := s.forum.SetContractsForumChannelID(ctx, serverID, vals[0]); err != nil {
			return fmt.Errorf("contracts: set forum channel: %w", err)
		}
	}
	return r.UpdateComponentsV2(i.Interaction, rerender())
}
