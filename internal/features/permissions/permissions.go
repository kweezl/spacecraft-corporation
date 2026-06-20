// Package permissions implements role-based command access control. A server
// admin maps Discord roles to commands; a member may run a command if they hold
// any mapped role (any-of). Commands with no mapping fall back to their policy:
// a "required" command (registry.Command.DefaultDeny) is denied, an "optional"
// one is allowed. The server owner and administrators always pass — the gate is
// bypassed for them in the session before it is consulted.
//
// The feature contributes the access Gate (the session's CommandAccess) and the
// /permissions panel used to manage the mapping (see panel.go). When the feature
// is disabled no gate is provided, so the session allows every command (role
// control off). Mappings are isolated per server: every query is keyed by the
// server ID, and Discord role IDs are themselves per-guild.
package permissions

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
)

// commandName is the management command's name. It is itself access-controlled
// (DefaultDeny), so it is owner/admin-only until the owner grants a role access
// to it — that is how the owner delegates permission management.
const commandName = "permissions"

// Mapping is one (command, role) grant for a server.
type Mapping struct {
	Command string
	RoleID  string
}

// Repository persists the per-server command→role mapping. serverID is the
// resolved servers.id.
type Repository interface {
	// RolesFor returns the role IDs granted access to a command on a server.
	RolesFor(ctx context.Context, serverID uuid.UUID, command string) ([]string, error)
	// SetRoles replaces a command's granted roles with exactly roleIDs, in one
	// transaction (an empty slice clears the command). createdByUserID is recorded
	// on newly inserted grants.
	SetRoles(ctx context.Context, serverID uuid.UUID, command string, roleIDs []string, createdByUserID string) error
	// List returns every mapping on a server, for the panel and the gate cache.
	List(ctx context.Context, serverID uuid.UUID) ([]Mapping, error)
}

// Gate answers the session's CommandAccess check from the role mapping, reading
// through the Store's per-server cache.
type Gate struct {
	store *Store
}

// NewGate builds the access gate over the cached store.
func NewGate(store *Store) *Gate { return &Gate{store: store} }

// IsAllowed grants access when the user holds any mapped role; with no mapping
// it honours the command's default policy (deny for required, allow for
// optional). Owner/admin bypass happens in the session before this is called.
// Membership is a direct lookup against the cached role set — O(user roles),
// no per-call allocation.
func (g *Gate) IsAllowed(ctx context.Context, req session.AccessRequest) (bool, error) {
	mapped, err := g.store.roleSet(ctx, req.ServerID, req.Command)
	if err != nil {
		return false, fmt.Errorf("permissions: roles for %q: %w", req.Command, err)
	}
	if len(mapped) == 0 {
		return !req.DefaultDeny, nil
	}
	for _, r := range req.UserRoles {
		if _, ok := mapped[r]; ok {
			return true, nil
		}
	}
	return false, nil
}
