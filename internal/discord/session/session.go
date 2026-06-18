// Package session opens one discordgo session per encrypted bot token loaded
// from Postgres and routes interactions through the shared command registry.
package session

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/kweezl/spacecraft-cadet/internal/config"
	"github.com/kweezl/spacecraft-cadet/internal/discord/registry"
	"github.com/kweezl/spacecraft-cadet/internal/token"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config is this module's env config.
type Config struct {
	Scope      string `env:"COMMAND_SCOPE" envDefault:"guild"`
	DevGuildID string `env:"DEV_GUILD_ID"`
}

// Discord is the slice of a Discord session the manager uses. The real
// implementation wraps *discordgo.Session; tests use a fake.
type Discord interface {
	registry.Responder
	AddInteractionHandler(fn func(*discordgo.InteractionCreate))
	CreateCommand(guildID string, cmd *discordgo.ApplicationCommand) error
	Open() error
	Close() error
}

// Factory builds a Discord session for a bot token.
type Factory func(token string) (Discord, error)

// Manager owns all live sessions.
type Manager struct {
	cfg      Config
	tokens   token.Repository
	registry *registry.Registry
	factory  Factory
	log      *zap.Logger

	sessions []Discord
}

func newManager(cfg Config, tokens token.Repository, reg *registry.Registry, factory Factory, log *zap.Logger) *Manager {
	return &Manager{cfg: cfg, tokens: tokens, registry: reg, factory: factory, log: log}
}

// commandGuildID returns the guild to register commands against: the dev guild
// for "guild" scope, or "" (global) otherwise.
func (m *Manager) commandGuildID() string {
	if m.cfg.Scope == "guild" {
		return m.cfg.DevGuildID
	}
	return ""
}

// Start loads tokens, opens a session per token, registers commands, and wires
// the interaction handler.
func (m *Manager) Start(ctx context.Context) error {
	toks, err := m.tokens.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("list tokens: %w", err)
	}
	guildID := m.commandGuildID()

	for _, tok := range toks {
		d, err := m.factory(tok.Token)
		if err != nil {
			return fmt.Errorf("create session for guild %s: %w", tok.GuildID, err)
		}

		d.AddInteractionHandler(func(i *discordgo.InteractionCreate) {
			if i.Type != discordgo.InteractionApplicationCommand {
				return
			}
			if derr := m.registry.Dispatch(ctx, d, i); derr != nil {
				m.log.Error("dispatch interaction", zap.Error(derr))
			}
		})

		if err := d.Open(); err != nil {
			return fmt.Errorf("open session for guild %s: %w", tok.GuildID, err)
		}
		for _, cmd := range m.registry.Commands() {
			if err := d.CreateCommand(guildID, cmd); err != nil {
				return fmt.Errorf("register %q for guild %s: %w", cmd.Name, tok.GuildID, err)
			}
		}
		m.sessions = append(m.sessions, d)
		m.log.Info("session started", zap.String("guild_id", tok.GuildID))
	}
	return nil
}

// Stop closes all sessions.
func (m *Manager) Stop(context.Context) error {
	for _, s := range m.sessions {
		_ = s.Close()
	}
	return nil
}

func register(lc fx.Lifecycle, m *Manager) {
	lc.Append(fx.Hook{OnStart: m.Start, OnStop: m.Stop})
}

// Module provides the Manager and runs it via the fx lifecycle. Its OnStart
// hook runs after the migrator invoke, so the schema already exists.
var Module = fx.Module("session",
	fx.Provide(config.Parse[Config]),
	fx.Provide(NewFactory),
	fx.Provide(newManager),
	fx.Invoke(register),
)
