// Package ping implements the /ping slash command. It records each invocation
// in Postgres and replies with the running count for the guild.
package ping

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// Repository persists ping invocations. serverID is the resolved servers.id.
type Repository interface {
	Record(ctx context.Context, serverID uuid.UUID, userID string) error
	Count(ctx context.Context, serverID uuid.UUID) (int64, error)
}

// NewCommand builds the /ping command. Enable/disable is decided at the module
// level (see Module), so this always returns a command. (Command-call counts
// are recorded centrally by the registry as discord_command_total.)
func NewCommand(repo Repository, loc *i18n.Localizer) *registry.Command {
	return &registry.Command{
		Def: &discordgo.ApplicationCommand{
			Name:        "ping",
			Description: "Replies with pong and the running ping count",
		},
		Handler: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
			userID := interactionUserID(i)
			if err := repo.Record(ctx, serverID, userID); err != nil {
				return fmt.Errorf("record ping: %w", err)
			}
			count, err := repo.Count(ctx, serverID)
			if err != nil {
				return fmt.Errorf("count pings: %w", err)
			}
			return r.Respond(i.Interaction,
				loc.Render(ctx, serverID, "ping.pong", map[string]any{"Count": count}))
		},
	}
}

// interactionUserID returns the invoking user's ID, handling both guild
// (i.Member) and DM (i.User) contexts.
func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}
