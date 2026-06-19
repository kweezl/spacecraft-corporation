// Package servers tracks the Discord servers (guilds) the bot belongs to. On
// join it records the server (auto-approving those in APPROVED_SERVER_ID),
// keeps an append-only event log of membership changes, and answers approval
// checks so the session can ignore commands from unapproved servers.
package servers

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
)

// Event types recorded in server_event.
const (
	EventJoined  = "joined"
	EventRemoved = "removed"
)

// defaultCacheSize bounds how many servers' resolutions are held in memory; the
// LRU evicts the least-recently-used beyond this. Same caching model as
// permissions.Store / settings.Store (single process, no cross-process writers).
const defaultCacheSize = 1000

// Server is a resolved servers row: its UUID primary key and approval status.
// The session resolves the Discord snowflake to this once per interaction, then
// passes ID downstream so repositories key directly on servers_id.
type Server struct {
	ID       uuid.UUID
	Approved bool
}

// Config is this module's env config. APPROVED_SERVER_ID is a comma-separated
// allowlist of Discord server (guild) IDs that are auto-approved on join. It may
// be empty (nothing is auto-approved). The list can only *promote* a server to
// approved; it never demotes one, so a manual approval is never overwritten.
type Config struct {
	ApprovedServerID []string `env:"APPROVED_SERVER_ID" envSeparator:","`
}

// Repository persists server records and the membership event log. It deals in
// primitives (not the Server struct) so the generated mock doesn't import this
// package — the in-package test uses that mock, which would otherwise cycle.
type Repository interface {
	// Upsert inserts a new server (approved set from inList) or, for an existing
	// row, refreshes the name and promotes approved to true when inList — never
	// demoting it. It returns the resulting row's id and approval plus whether a
	// new row was inserted, so the caller can prime its cache without a re-read.
	Upsert(ctx context.Context, serverID, name string, inList bool) (id uuid.UUID, approved, isNew bool, err error)
	// LogEvent appends a membership event for a server.
	LogEvent(ctx context.Context, serverID, eventType string) error
	// Get resolves a Discord snowflake to its servers row id and approval. found
	// reports whether the server exists; an unknown server yields (uuid.Nil,
	// false, false, nil).
	Get(ctx context.Context, serverID string) (id uuid.UUID, approved, found bool, err error)
}

// Manager reacts to guild lifecycle events, resolves Discord snowflakes to
// servers rows (LRU-cached), and answers the session's approval check.
type Manager struct {
	repo     Repository
	log      *zap.Logger
	approved map[string]bool
	// cache fronts Get on the interaction hot path: snowflake -> resolved Server.
	cache *lru.Cache[string, Server]
}

func newManager(cfg Config, repo Repository, log *zap.Logger) (*Manager, error) {
	set := make(map[string]bool, len(cfg.ApprovedServerID))
	for _, id := range cfg.ApprovedServerID {
		if id = strings.TrimSpace(id); id != "" {
			set[id] = true
		}
	}
	cache, err := lru.New[string, Server](defaultCacheSize)
	if err != nil {
		return nil, fmt.Errorf("servers: new cache: %w", err)
	}
	return &Manager{repo: repo, log: log, approved: set, cache: cache}, nil
}

// OnGuildCreate records the server, auto-approving allowlisted ones, and logs a
// join event the first time we see it. Discord fires GuildCreate for every server
// on connect (not only genuine joins), so it looks the server up (cached) first
// and only writes when something actually changed: a brand-new server (insert +
// join log) or an allowlisted server not yet approved (promote). A known,
// already-correctly-approved server takes no DB write on reconnect — only its
// (display-only) name can go briefly stale until the next cache miss.
func (m *Manager) OnGuildCreate(g *discordgo.GuildCreate) {
	ctx := context.Background()
	inList := m.approved[g.ID]

	s, found, err := m.serverFor(ctx, g.ID)
	if err != nil {
		m.log.Error("resolve server", zap.String("server_id", g.ID), zap.Error(err))
		return
	}

	if found {
		// Promote an allowlisted server that isn't approved yet; otherwise nothing
		// to do (the common every-reconnect path skips the DB entirely).
		if inList && !s.Approved {
			id, approved, _, uerr := m.repo.Upsert(ctx, g.ID, g.Name, true)
			if uerr != nil {
				m.log.Error("promote server", zap.String("server_id", g.ID), zap.Error(uerr))
				return
			}
			m.cache.Add(g.ID, Server{ID: id, Approved: approved})
		}
		return
	}

	// First time we've seen this server: insert it and prime the cache.
	id, approved, isNew, err := m.repo.Upsert(ctx, g.ID, g.Name, inList)
	if err != nil {
		m.log.Error("upsert server", zap.String("server_id", g.ID), zap.Error(err))
		return
	}
	m.cache.Add(g.ID, Server{ID: id, Approved: approved})
	if !isNew {
		// A concurrent GuildCreate inserted it first; the join was already logged.
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

// serverFor resolves a Discord snowflake to its Server, fronting repo.Get with
// the LRU cache. Positive lookups are cached; a not-found result is not cached
// (it is rare and would otherwise survive the row's later creation — though
// OnGuildCreate also primes the cache on insert).
func (m *Manager) serverFor(ctx context.Context, serverID string) (Server, bool, error) {
	if s, ok := m.cache.Get(serverID); ok {
		return s, true, nil
	}
	id, approved, found, err := m.repo.Get(ctx, serverID)
	if err != nil {
		return Server{}, false, err
	}
	if !found {
		return Server{}, false, nil
	}
	s := Server{ID: id, Approved: approved}
	m.cache.Add(serverID, s)
	return s, true, nil
}

// Resolve resolves a Discord snowflake to its servers.id and approval status; it
// satisfies the session's ServerResolver. An unresolvable server (no row yet, or
// a lookup error) yields (uuid.Nil, false) so the session treats it as not
// approved.
func (m *Manager) Resolve(ctx context.Context, serverID string) (uuid.UUID, bool, error) {
	s, found, err := m.serverFor(ctx, serverID)
	if err != nil {
		return uuid.Nil, false, err
	}
	if !found {
		return uuid.Nil, false, nil
	}
	return s.ID, s.Approved, nil
}
