// Package ping implements the /ping slash command. It records each invocation
// in Postgres and replies with the running count for the guild.
package ping

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/kweezl/spacecraft-cadet/internal/config"
	"github.com/kweezl/spacecraft-cadet/internal/discord/registry"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config is this feature's env config.
type Config struct {
	Enabled bool `env:"FEATURE_PING_ENABLED" envDefault:"true"`
}

// Repository persists ping invocations.
type Repository interface {
	Record(ctx context.Context, guildID, userID string) error
	Count(ctx context.Context, guildID string) (int64, error)
}

// NewCommand returns the /ping command, or nil when the feature is disabled.
// A nil command is skipped by the registry, which is how modules disable.
func NewCommand(cfg Config, repo Repository, log *zap.Logger) *registry.Command {
	if !cfg.Enabled {
		log.Info("ping feature disabled")
		return nil
	}
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

// Module contributes the /ping command into the registry's "commands" group.
var Module = fx.Module("ping",
	fx.Provide(config.Parse[Config]),
	fx.Provide(newRepository),
	fx.Provide(fx.Annotate(
		NewCommand,
		fx.ResultTags(`group:"commands"`),
	)),
)
