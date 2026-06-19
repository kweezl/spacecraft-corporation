// Package servers tracks the Discord servers (guilds) the bot belongs to. On
// join it records the server (auto-approving those in APPROVED_SERVER_ID),
// keeps an append-only event log of membership changes, and answers approval
// checks so the session can ignore commands from unapproved servers.
package servers

import (
	"context"
	"strings"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

// Event types recorded in server_event.
const (
	EventJoined  = "joined"
	EventRemoved = "removed"
)

// Config is this module's env config. APPROVED_SERVER_ID is a comma-separated
// allowlist of Discord server (guild) IDs that are auto-approved on join. It may
// be empty (nothing is auto-approved). The list can only *promote* a server to
// approved; it never demotes one, so a manual approval is never overwritten.
type Config struct {
	ApprovedServerID []string `env:"APPROVED_SERVER_ID" envSeparator:","`
}

// Repository persists server records and the membership event log.
type Repository interface {
	// Upsert inserts a new server (approved set from inList) or, for an existing
	// row, refreshes the name and promotes approved to true when inList — never
	// demoting it. It reports whether a new row was inserted.
	Upsert(ctx context.Context, serverID, name string, inList bool) (isNew bool, err error)
	// LogEvent appends a membership event for a server.
	LogEvent(ctx context.Context, serverID, eventType string) error
	// IsApproved reports whether a server is approved. An unknown server is not
	// approved.
	IsApproved(ctx context.Context, serverID string) (bool, error)
}

// Manager reacts to guild lifecycle events and answers approval checks.
type Manager struct {
	repo     Repository
	log      *zap.Logger
	approved map[string]bool
}

func newManager(cfg Config, repo Repository, log *zap.Logger) *Manager {
	set := make(map[string]bool, len(cfg.ApprovedServerID))
	for _, id := range cfg.ApprovedServerID {
		if id = strings.TrimSpace(id); id != "" {
			set[id] = true
		}
	}
	return &Manager{repo: repo, log: log, approved: set}
}

// OnGuildCreate records the server, auto-approving allowlisted ones, and logs a
// join event the first time we see it. Discord fires GuildCreate for every
// server on connect (not only genuine joins), so the event is logged only for
// servers not already in the database.
func (m *Manager) OnGuildCreate(g *discordgo.GuildCreate) {
	ctx := context.Background()
	inList := m.approved[g.ID]
	isNew, err := m.repo.Upsert(ctx, g.ID, g.Name, inList)
	if err != nil {
		m.log.Error("upsert server", zap.String("server_id", g.ID), zap.Error(err))
		return
	}
	if !isNew {
		return
	}
	if err := m.repo.LogEvent(ctx, g.ID, EventJoined); err != nil {
		m.log.Error("log join event", zap.String("server_id", g.ID), zap.Error(err))
	}
	m.log.Info("joined server",
		zap.String("server_id", g.ID), zap.String("name", g.Name), zap.Bool("approved", inList))
}

// OnGuildDelete logs a removal when the bot is genuinely removed from a server.
// A GuildDelete with Unavailable set is a transient gateway outage, not a
// removal, so it is ignored.
func (m *Manager) OnGuildDelete(g *discordgo.GuildDelete) {
	if g.Unavailable {
		return
	}
	if err := m.repo.LogEvent(context.Background(), g.ID, EventRemoved); err != nil {
		m.log.Error("log removal event", zap.String("server_id", g.ID), zap.Error(err))
	}
	m.log.Info("removed from server", zap.String("server_id", g.ID))
}

// IsApproved reports whether the server is approved; it satisfies the session's
// approval gate.
func (m *Manager) IsApproved(ctx context.Context, serverID string) (bool, error) {
	return m.repo.IsApproved(ctx, serverID)
}
