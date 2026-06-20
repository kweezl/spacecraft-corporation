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
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/appconfig"
	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
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

// ServerResolver resolves a Discord snowflake to its servers.id (the UUID primary
// key) and approval status, in one cached lookup. The session resolves every
// interaction's guild through it once: commands from a server that is not approved
// (or cannot be resolved) are not dispatched, and the resolved id is threaded
// downstream to handlers. Provided by the servers module.
type ServerResolver interface {
	Resolve(ctx context.Context, serverID string) (id uuid.UUID, approved bool, err error)
}

// CommandAccess decides whether an interaction may run a command, based on the
// server's per-command role mapping. It is provided optionally by the
// permissions feature; when that feature is disabled the gate is nil and every
// command is allowed (role control off entirely). Owners and administrators
// bypass it before it is consulted (see Manager.allowed).
type CommandAccess interface {
	IsAllowed(ctx context.Context, req AccessRequest) (bool, error)
}

// AccessRequest is what the access gate needs to decide. ServerID is the resolved
// servers.id; UserRoles are the invoking member's Discord role IDs in this server;
// DefaultDeny is the command's policy when the server has no role mapping for it.
type AccessRequest struct {
	ServerID    uuid.UUID
	Command     string
	UserRoles   []string
	DefaultDeny bool
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
	// Connected reports whether the gateway connection is live and the initial
	// READY handshake has completed (discordgo's DataReady). Open() returns
	// before READY arrives, so this is what readiness actually waits on.
	Connected() bool

	// Proactive operations used outside the interaction-response path (the
	// Responder above only replies to interactions). Contracts posts each
	// contract as a forum thread and edits/locks it as progress and the deadline
	// change; these wrap the matching *discordgo.Session calls.
	ForumThreadStartComplex(channelID string, threadData *discordgo.ThreadStart, messageData *discordgo.MessageSend) (*discordgo.Channel, error)
	ChannelMessageEditComplex(m *discordgo.MessageEdit) (*discordgo.Message, error)
	ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend) (*discordgo.Message, error)
	ChannelEditComplex(channelID string, data *discordgo.ChannelEdit) (*discordgo.Channel, error)
	// InteractionResponseEdit edits an interaction's original reply via the
	// webhook identified by the interaction's app id + token (used to deliver an
	// async outcome after the initial ack).
	InteractionResponseEdit(i *discordgo.Interaction, edit *discordgo.WebhookEdit) (*discordgo.Message, error)
}

// Factory builds a Discord session for a bot token.
type Factory func(token string) (Discord, error)

// Manager owns the bot's session.
type Manager struct {
	cfg           Config
	registry      *registry.Registry
	factory       Factory
	resolver      ServerResolver
	access        CommandAccess
	loc           *i18n.Localizer
	onGuildCreate []GuildCreateFunc
	onGuildDelete []GuildDeleteFunc
	log           *zap.Logger
	ownerID       string // bot owner's Discord ID, surfaced in the unapproved reply

	// live holds the open session (published on Start, cleared on Stop). It is
	// shared with proactive callers (e.g. contracts) and the readiness probe, and
	// guards concurrent access itself.
	live *Live
	// baseCtx scopes per-interaction work to the session's lifetime; cancelled on
	// Stop. NOT the fx OnStart context, which is done once Start returns.
	baseCtx context.Context
	cancel  context.CancelFunc
}

func newManager(
	cfg Config,
	reg *registry.Registry,
	factory Factory,
	resolver ServerResolver,
	access CommandAccess,
	loc *i18n.Localizer,
	onGuildCreate []GuildCreateFunc,
	onGuildDelete []GuildDeleteFunc,
	log *zap.Logger,
	appCfg appconfig.AppConfig,
	live *Live,
) *Manager {
	return &Manager{
		cfg:           cfg,
		registry:      reg,
		factory:       factory,
		resolver:      resolver,
		access:        access,
		loc:           loc,
		onGuildCreate: onGuildCreate,
		onGuildDelete: onGuildDelete,
		log:           log,
		ownerID:       appCfg.OwnerDiscordID,
		live:          live,
	}
}

// resolve looks up a server's id and approval status. A nil resolver (e.g. in
// tests) approves everything with a nil id; a resolver error is treated as
// not-approved and logged. The returned id is uuid.Nil when the server is
// unapproved or unresolvable, which renders the reply with app defaults.
func (m *Manager) resolve(ctx context.Context, serverID string) (uuid.UUID, bool) {
	if m.resolver == nil {
		return uuid.Nil, true
	}
	id, approved, err := m.resolver.Resolve(ctx, serverID)
	if err != nil {
		m.log.Error("server resolution", zap.String("server_id", serverID), zap.Error(err))
		return uuid.Nil, false
	}
	return id, approved
}

// allowed reports whether the interaction may run its command. A nil access gate
// (permissions feature disabled) allows everything. The server owner and any
// administrator bypass the gate — Discord folds the owner's implicit
// all-permissions into the member's computed permissions, so the Administrator
// bit covers both. Otherwise the gate decides from the server's role mapping;
// a gate error fails closed (denied) and is logged.
func (m *Manager) allowed(ctx context.Context, i *discordgo.InteractionCreate, serverID uuid.UUID) bool {
	if m.access == nil {
		return true
	}
	if isAdministrator(i.Member) {
		return true
	}
	name := i.ApplicationCommandData().Name
	// Policy is a property of the whole command (one DefaultDeny per command),
	// so it keys on the top-level name; the grant key may be finer (the
	// subcommand path) for SubcommandGated commands.
	defaultDeny, known := m.registry.Policy(name)
	if !known {
		// Dispatch will reject an unknown command; don't gate it here.
		return true
	}
	key := m.registry.AccessKey(i)
	var roles []string
	if i.Member != nil {
		roles = i.Member.Roles
	}
	ok, err := m.access.IsAllowed(ctx, AccessRequest{
		ServerID:    serverID,
		Command:     key,
		UserRoles:   roles,
		DefaultDeny: defaultDeny,
	})
	if err != nil {
		m.log.Error("access check",
			zap.String("server_id", i.GuildID), zap.String("command", key), zap.Error(err))
		return false
	}
	return ok
}

// handleCommand runs the application-command path: reply with the approval
// notice for unapproved servers, enforce the role gate, then dispatch.
func (m *Manager) handleCommand(ctx context.Context, d Discord, i *discordgo.InteractionCreate, serverID uuid.UUID, approved bool) {
	if !approved {
		m.log.Debug("command from unapproved server", zap.String("server_id", i.GuildID))
		msg := m.loc.Render(ctx, serverID, "session.unapproved", map[string]any{"Owner": m.ownerID})
		if rerr := d.Respond(i.Interaction, msg); rerr != nil {
			m.log.Error("respond to unapproved server",
				zap.String("server_id", i.GuildID), zap.Error(rerr))
		}
		return
	}
	if !m.allowed(ctx, i, serverID) {
		key := m.registry.AccessKey(i)
		m.log.Debug("command blocked by role gate",
			zap.String("server_id", i.GuildID), zap.String("command", key))
		msg := m.loc.Render(ctx, serverID, "session.denied", map[string]any{"Command": key})
		if rerr := d.Respond(i.Interaction, msg); rerr != nil {
			m.log.Error("respond to blocked command",
				zap.String("server_id", i.GuildID), zap.Error(rerr))
		}
		return
	}
	if derr := m.registry.Dispatch(ctx, d, i, serverID); derr != nil {
		m.log.Error("dispatch interaction", zap.Error(derr))
	}
}

// isAdministrator reports whether the member has Discord's Administrator
// permission. The computed Permissions on the interaction already fold in the
// guild owner's implicit all-permissions, so this is true for the owner too.
func isAdministrator(member *discordgo.Member) bool {
	return member != nil && member.Permissions&discordgo.PermissionAdministrator != 0
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
		// All interactions are guild-only; ignore DMs.
		if i.GuildID == "" {
			return
		}
		// Per-interaction context: scoped to the session lifetime and bounded so
		// a slow query can't hang the handler.
		ctx, cancel := context.WithTimeout(m.baseCtx, interactionTimeout)
		defer cancel()
		// Resolve the snowflake to the servers.id once; thread it downstream so
		// nothing re-resolves it per query. uuid.Nil (unapproved/unresolvable)
		// renders replies with app defaults.
		serverID, approved := m.resolve(ctx, i.GuildID)

		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			m.handleCommand(ctx, d, i, serverID, approved)
		case discordgo.InteractionApplicationCommandAutocomplete:
			// Don't suggest anything to an unapproved server; otherwise let the
			// command's handler scope its own suggestions (autocomplete is not an
			// authorization boundary — the value is re-validated on submit).
			if !approved {
				_ = d.RespondAutocomplete(i.Interaction, nil)
				return
			}
			if derr := m.registry.DispatchAutocomplete(ctx, d, i, serverID); derr != nil {
				m.log.Error("dispatch autocomplete", zap.Error(derr))
			}
		case discordgo.InteractionMessageComponent:
			// Component handlers (e.g. pagination) are read-only and re-authorize
			// any mutation themselves; gate only on server approval here.
			if !approved {
				return
			}
			if derr := m.registry.DispatchComponent(ctx, d, i, serverID); derr != nil {
				m.log.Error("dispatch component", zap.Error(derr))
			}
		default:
			// Other interaction types (e.g. modal submit) are not handled.
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

	m.live.set(d)
	m.log.Info("session started")
	return nil
}

// Stop cancels in-flight interaction work and closes the session.
func (m *Manager) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	if s := m.live.get(); s != nil {
		_ = s.Close()
		m.live.set(nil)
	}
	return nil
}

// Connected reports whether the session exists and its gateway is ready. It
// backs the "discord" readiness probe.
func (m *Manager) Connected() bool {
	return m.live.Connected()
}

func register(lc fx.Lifecycle, m *Manager) {
	lc.Append(fx.Hook{OnStart: m.Start, OnStop: m.Stop})
}
