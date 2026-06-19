package permissions

import (
	"context"
	"fmt"

	lru "github.com/hashicorp/golang-lru/v2"
)

// defaultCacheSize bounds how many servers' role maps are held in memory. The
// LRU evicts the least-recently-used server beyond this, so inactive servers
// unload on their own; an evicted server is simply reloaded on its next gated
// interaction.
const defaultCacheSize = 1000

// serverRoles is one server's mapping: command name -> set of role IDs granted
// it. Built once on load and treated as immutable thereafter, so concurrent
// gate reads need no locking beyond the cache's own.
type serverRoles map[string]map[string]struct{}

// Store fronts the Repository with an in-memory, per-server LRU cache of role
// mappings for the access gate's hot path (a check on every gated interaction).
// Writes pass through to the database and invalidate the affected server, so the
// cache stays coherent. It is coherent within this process only — the bot runs
// as a single process (one session), so there are no other writers; a horizontal
// scale-out would need cross-process invalidation instead.
//
// Admin reads (RolesFor/List, used by /permissions) bypass the cache and hit the
// database directly, so management output is always fresh.
type Store struct {
	repo  Repository
	cache *lru.Cache[string, serverRoles]
}

// NewStore wraps a Repository with the role cache.
func NewStore(repo Repository) (*Store, error) {
	c, err := lru.New[string, serverRoles](defaultCacheSize)
	if err != nil {
		return nil, fmt.Errorf("permissions: new cache: %w", err)
	}
	return &Store{repo: repo, cache: c}, nil
}

// roleSet returns the set of role IDs mapped to a command on a server, loading
// (and caching) the server's full mapping on a miss. The returned map is shared
// and must not be mutated by the caller.
func (s *Store) roleSet(ctx context.Context, serverID, command string) (map[string]struct{}, error) {
	if sr, ok := s.cache.Get(serverID); ok {
		return sr[command], nil
	}
	sr, err := s.load(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return sr[command], nil
}

// load reads every mapping for a server in one query and caches it. Loading the
// whole server (not just one command) means subsequent commands for an active
// server are served from memory. An unmapped server caches an empty map, so it
// is not re-queried on every interaction (negative caching).
func (s *Store) load(ctx context.Context, serverID string) (serverRoles, error) {
	all, err := s.repo.List(ctx, serverID)
	if err != nil {
		return nil, err
	}
	sr := make(serverRoles)
	for _, m := range all {
		set := sr[m.Command]
		if set == nil {
			set = make(map[string]struct{})
			sr[m.Command] = set
		}
		set[m.RoleID] = struct{}{}
	}
	s.cache.Add(serverID, sr)
	return sr, nil
}

// invalidate drops a server's cached mapping so the next read reloads it.
func (s *Store) invalidate(serverID string) { s.cache.Remove(serverID) }

// Store implements Repository: writes pass through to the DB and invalidate the
// server's cache; admin reads (RolesFor/List) bypass the cache for freshness.

// RolesFor returns a command's mapped roles straight from the database.
func (s *Store) RolesFor(ctx context.Context, serverID, command string) ([]string, error) {
	return s.repo.RolesFor(ctx, serverID, command)
}

// List returns every mapping on a server straight from the database.
func (s *Store) List(ctx context.Context, serverID string) ([]Mapping, error) {
	return s.repo.List(ctx, serverID)
}

// Grant persists a role grant and invalidates the server's cached mapping.
func (s *Store) Grant(ctx context.Context, serverID, command, roleID, createdByUserID string) error {
	if err := s.repo.Grant(ctx, serverID, command, roleID, createdByUserID); err != nil {
		return err
	}
	s.invalidate(serverID)
	return nil
}

// Revoke removes a role grant and invalidates the server's cached mapping.
func (s *Store) Revoke(ctx context.Context, serverID, command, roleID string) error {
	if err := s.repo.Revoke(ctx, serverID, command, roleID); err != nil {
		return err
	}
	s.invalidate(serverID)
	return nil
}

// Clear removes all role grants for a command and invalidates the server's cache.
func (s *Store) Clear(ctx context.Context, serverID, command string) error {
	if err := s.repo.Clear(ctx, serverID, command); err != nil {
		return err
	}
	s.invalidate(serverID)
	return nil
}
