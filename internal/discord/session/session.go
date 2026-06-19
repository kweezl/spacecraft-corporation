// Package session opens the bot's single discordgo session from BOT_TOKEN and
// routes interactions through the shared command registry.
package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config is this module's env config.
//
// The bot token is a secret. Provide it directly via BOT_TOKEN (convenient for
// dev), or point BOT_TOKEN_FILE at a mounted secret file — the ",file" option
// makes env read that file's contents. Prefer the file in prod (Docker/K8s
// secret): files can be 0400, live in tmpfs, and don't leak via `docker inspect`
// or /proc/<pid>/environ the way env vars can. TokenFile wins if both are set.
type Config struct {
	Token       string `env:"BOT_TOKEN"`
	TokenFile   string `env:"BOT_TOKEN_FILE,file"`
	Scope       string `env:"COMMAND_SCOPE" envDefault:"server"`
	DevServerID string `env:"DEV_SERVER_ID"`
}

// botToken resolves the token, preferring the file-mounted secret. Whitespace
// is trimmed so a trailing newline in a secret file doesn't corrupt the token.
func (c Config) botToken() (string, error) {
	if t := strings.TrimSpace(c.TokenFile); t != "" {
		return t, nil
	}
	if t := strings.TrimSpace(c.Token); t != "" {
		return t, nil
	}
	return "", errors.New("session: set BOT_TOKEN or BOT_TOKEN_FILE")
}

// Discord is the slice of a Discord session the manager uses. The real
// implementation wraps *discordgo.Session; tests use a fake.
type Discord interface {
	registry.Responder
	AddInteractionHandler(fn func(*discordgo.InteractionCreate))
	CreateCommand(serverID string, cmd *discordgo.ApplicationCommand) error
	Open() error
	Close() error
}

// Factory builds a Discord session for a bot token.
type Factory func(token string) (Discord, error)

// Manager owns the bot's session.
type Manager struct {
	cfg      Config
	registry *registry.Registry
	factory  Factory
	log      *zap.Logger

	session Discord
}

func newManager(cfg Config, reg *registry.Registry, factory Factory, log *zap.Logger) *Manager {
	return &Manager{cfg: cfg, registry: reg, factory: factory, log: log}
}

// commandServerID returns the server to register commands against: the dev
// server for "server" scope, or "" (global) otherwise.
func (m *Manager) commandServerID() string {
	if m.cfg.Scope == "server" {
		return m.cfg.DevServerID
	}
	return ""
}

// Start opens the session, wires the interaction handler, and registers
// commands.
func (m *Manager) Start(ctx context.Context) error {
	token, err := m.cfg.botToken()
	if err != nil {
		return err
	}
	d, err := m.factory(token)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
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
		return fmt.Errorf("open session: %w", err)
	}

	serverID := m.commandServerID()
	for _, cmd := range m.registry.Commands() {
		if err := d.CreateCommand(serverID, cmd); err != nil {
			return fmt.Errorf("register %q: %w", cmd.Name, err)
		}
	}

	m.session = d
	m.log.Info("session started", zap.String("scope", m.cfg.Scope))
	return nil
}

// Stop closes the session.
func (m *Manager) Stop(context.Context) error {
	if m.session != nil {
		_ = m.session.Close()
	}
	return nil
}

func register(lc fx.Lifecycle, m *Manager) {
	lc.Append(fx.Hook{OnStart: m.Start, OnStop: m.Stop})
}
