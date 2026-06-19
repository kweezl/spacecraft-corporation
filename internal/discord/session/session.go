// Package session opens the bot's single discordgo session from BOT_TOKEN and
// routes interactions through the shared command registry.
package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/appconfig"
	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// interactionTimeout bounds the work done per interaction (approval lookup +
// dispatch). Discord separately requires an initial response within ~3s; this is
// just a safety net so a hung DB or API call can't leak a handler goroutine.
const interactionTimeout = 10 * time.Second

// Config is this module's env config.
//
// The bot token is a secret. Provide it directly via BOT_TOKEN (convenient for
// dev), or point BOT_TOKEN_FILE at a mounted secret file — the ",file" option
// makes env read that file's contents. Prefer the file in prod (Docker/K8s
// secret): files can be 0400, live in tmpfs, and don't leak via `docker inspect`
// or /proc/<pid>/environ the way env vars can. TokenFile wins if both are set.
type Config struct {
	Token     string `env:"BOT_TOKEN"`
	TokenFile string `env:"BOT_TOKEN_FILE,file"`
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

// ServerApproval gates which servers (guilds) the bot serves. Commands from a
// server that is not approved are ignored. Provided by the servers module.
type ServerApproval interface {
	IsApproved(ctx context.Context, serverID string) (bool, error)
}

// Guild lifecycle reactions contributed by other modules (via fx groups) and
// attached to the session on start.
type (
	// GuildCreateFunc reacts to a guild becoming available (a join, or a
	// re-sync on connect).
	GuildCreateFunc func(*discordgo.GuildCreate)
	// GuildDeleteFunc reacts to the bot leaving or being removed from a guild.
	GuildDeleteFunc func(*discordgo.GuildDelete)
)

// Discord is the slice of a Discord session the manager uses. The real
// implementation wraps *discordgo.Session; tests use a fake.
type Discord interface {
	registry.Responder
	AddInteractionHandler(fn func(*discordgo.InteractionCreate))
	AddGuildCreateHandler(fn func(*discordgo.GuildCreate))
	AddGuildDeleteHandler(fn func(*discordgo.GuildDelete))
	CreateCommand(serverID string, cmd *discordgo.ApplicationCommand) error
	Open() error
	Close() error
}

// Factory builds a Discord session for a bot token.
type Factory func(token string) (Discord, error)

// Manager owns the bot's session.
type Manager struct {
	cfg           Config
	registry      *registry.Registry
	factory       Factory
	gate          ServerApproval
	onGuildCreate []GuildCreateFunc
	onGuildDelete []GuildDeleteFunc
	log           *zap.Logger
	ownerID       string // bot owner's Discord ID, surfaced in the unapproved reply

	session Discord
	// baseCtx scopes per-interaction work to the session's lifetime; cancelled on
	// Stop. NOT the fx OnStart context, which is done once Start returns.
	baseCtx context.Context
	cancel  context.CancelFunc
}

func newManager(
	cfg Config,
	reg *registry.Registry,
	factory Factory,
	gate ServerApproval,
	onGuildCreate []GuildCreateFunc,
	onGuildDelete []GuildDeleteFunc,
	log *zap.Logger,
	appCfg appconfig.AppConfig,
) *Manager {
	return &Manager{
		cfg:           cfg,
		registry:      reg,
		factory:       factory,
		gate:          gate,
		onGuildCreate: onGuildCreate,
		onGuildDelete: onGuildDelete,
		log:           log,
		ownerID:       appCfg.OwnerDiscordID,
	}
}

// unapprovedMessage tells the requester the server must be approved by the bot
// owner, mentioning the owner when APP_OWNER_DISCORD_ID is configured.
func (m *Manager) unapprovedMessage() string {
	if m.ownerID != "" {
		return fmt.Sprintf("This server isn't approved to use this bot yet. "+
			"Ask the bot owner <@%s> to approve it.", m.ownerID)
	}
	return "This server isn't approved to use this bot yet. Ask the bot owner to approve it."
}

// approved reports whether a server may be served. A nil gate (e.g. in tests)
// approves everything; a gate error is treated as not-approved and logged.
func (m *Manager) approved(ctx context.Context, serverID string) bool {
	if m.gate == nil {
		return true
	}
	ok, err := m.gate.IsApproved(ctx, serverID)
	if err != nil {
		m.log.Error("approval check", zap.String("server_id", serverID), zap.Error(err))
		return false
	}
	return ok
}

// registerCommands registers every command with one server (guild). Per-guild
// registration is instant (unlike global, which propagates over ~1h), so a
// newly joined server has the commands immediately. Re-registering on every
// GuildCreate is harmless: Discord upserts by command name.
func (m *Manager) registerCommands(d Discord, serverID string) {
	for _, cmd := range m.registry.Commands() {
		if err := d.CreateCommand(serverID, cmd); err != nil {
			m.log.Error("register command",
				zap.String("command", cmd.Name), zap.String("server_id", serverID), zap.Error(err))
		}
	}
}

// Start opens the session, wires the interaction handler, registers commands per
// joined server, and attaches the guild lifecycle handlers. The fx OnStart ctx
// is intentionally not retained: it is done once Start returns, whereas
// interactions arrive for the whole session lifetime (see baseCtx).
func (m *Manager) Start(context.Context) error {
	token, err := m.cfg.botToken()
	if err != nil {
		return err
	}
	d, err := m.factory(token)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	m.baseCtx, m.cancel = context.WithCancel(context.Background())

	d.AddInteractionHandler(func(i *discordgo.InteractionCreate) {
		if i.Type != discordgo.InteractionApplicationCommand {
			return
		}
		// Commands are guild-only; ignore DMs and unapproved servers.
		if i.GuildID == "" {
			return
		}
		// Per-interaction context: scoped to the session lifetime and bounded so
		// a slow query can't hang the handler.
		ctx, cancel := context.WithTimeout(m.baseCtx, interactionTimeout)
		defer cancel()
		if !m.approved(ctx, i.GuildID) {
			m.log.Debug("command from unapproved server", zap.String("server_id", i.GuildID))
			if rerr := d.Respond(i.Interaction, m.unapprovedMessage()); rerr != nil {
				m.log.Error("respond to unapproved server",
					zap.String("server_id", i.GuildID), zap.Error(rerr))
			}
			return
		}
		if derr := m.registry.Dispatch(ctx, d, i); derr != nil {
			m.log.Error("dispatch interaction", zap.Error(derr))
		}
	})

	// Register commands to each server as the bot joins it (GuildCreate also
	// fires for every existing server on connect).
	d.AddGuildCreateHandler(func(g *discordgo.GuildCreate) { m.registerCommands(d, g.ID) })

	// Guild lifecycle reactions contributed by other modules (e.g. servers).
	for _, fn := range m.onGuildCreate {
		d.AddGuildCreateHandler(fn)
	}
	for _, fn := range m.onGuildDelete {
		d.AddGuildDeleteHandler(fn)
	}

	if err := d.Open(); err != nil {
		return fmt.Errorf("open session: %w", err)
	}

	m.session = d
	m.log.Info("session started")
	return nil
}

// Stop cancels in-flight interaction work and closes the session.
func (m *Manager) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	if m.session != nil {
		_ = m.session.Close()
	}
	return nil
}

func register(lc fx.Lifecycle, m *Manager) {
	lc.Append(fx.Hook{OnStart: m.Start, OnStop: m.Stop})
}
