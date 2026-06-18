// Package ping implements the /ping slash command. It records each invocation
// in Postgres and replies with the running count for the guild.
package ping

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/kweezl/spacecraft-cadet/internal/discord/registry"
	"go.uber.org/fx"
)

// Repository persists ping invocations.
type Repository interface {
	Record(ctx context.Context, guildID, userID string) error
	Count(ctx context.Context, guildID string) (int64, error)
}

// NewCommand builds the /ping command. Enable/disable is decided at the module
// level (see Module), so this always returns a command. (Command-call counts
// are recorded centrally by the registry as discord_command_total.)
func NewCommand(repo Repository) *registry.Command {
	return &registry.Command{
		Def: &discordgo.ApplicationCommand{
			Name:        "ping",
			Description: "Replies with pong and the running ping count",
		},
		Handler: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate) error {
			guildID := i.GuildID
			userID := interactionUserID(i)
			if err := repo.Record(ctx, guildID, userID); err != nil {
				return fmt.Errorf("record ping: %w", err)
			}
			count, err := repo.Count(ctx, guildID)
			if err != nil {
				return fmt.Errorf("count pings: %w", err)
			}
			return r.Respond(i.Interaction, fmt.Sprintf("pong (#%d)", count))
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

// Module provides the /ping repository and contributes the command into the
// registry's "commands" group. Whether it loads at all is decided by the
// composition root (internal/app) from the FEATURES env var.
func Module() fx.Option {
	return fx.Module("ping",
		fx.Provide(newRepository),
		fx.Provide(fx.Annotate(
			NewCommand,
			fx.ResultTags(`group:"commands"`),
		)),
	)
}
