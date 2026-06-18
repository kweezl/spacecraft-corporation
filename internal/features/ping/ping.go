// Package ping implements the /ping slash command. It records each invocation
// in Postgres and replies with the running count for the guild.
package ping

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/kweezl/spacecraft-cadet/internal/discord/registry"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/fx"
)

// Repository persists ping invocations.
type Repository interface {
	Record(ctx context.Context, guildID, userID string) error
	Count(ctx context.Context, guildID string) (int64, error)
}

// newCounter creates and registers the /ping call counter.
func newCounter(reg *prometheus.Registry) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "spacecraft",
		Subsystem: "ping",
		Name:      "commands_total",
		Help:      "Total number of /ping commands handled.",
	})
	reg.MustRegister(c)
	return c
}

// NewCommand builds the /ping command. Enable/disable is decided at the module
// level (see Module), so this always returns a command. calls is incremented
// once per invocation.
func NewCommand(repo Repository, calls prometheus.Counter) *registry.Command {
	return &registry.Command{
		Def: &discordgo.ApplicationCommand{
			Name:        "ping",
			Description: "Replies with pong and the running ping count",
		},
		Handler: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate) error {
			calls.Inc()
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

// provideCommand wires the repository and a freshly registered call counter
// into the command, keeping prometheus.Counter out of the fx graph (so multiple
// features can't collide on the same provided type).
func provideCommand(repo Repository, reg *prometheus.Registry) *registry.Command {
	return NewCommand(repo, newCounter(reg))
}

// Module provides the /ping repository and contributes the command into the
// registry's "commands" group. Whether it loads at all is decided by the
// composition root (internal/app) from the FEATURES env var.
func Module() fx.Option {
	return fx.Module("ping",
		fx.Provide(newRepository),
		fx.Provide(fx.Annotate(
			provideCommand,
			fx.ResultTags(`group:"commands"`),
		)),
	)
}
