package permissions

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

type pgSuite struct{ testdb.Suite }

func TestPgRepository(t *testing.T) { suite.Run(t, new(pgSuite)) }

func (s *pgSuite) TestRepository() {
	t := s.T()
	ctx := context.Background()
	pool := s.Pool
	// permissions.servers_id references servers.id; SeedServer returns that id.
	g1 := testdb.SeedServer(t, pool, "g1")
	g2 := testdb.SeedServer(t, pool, "g2")

	repo := newRepository(pool)

	// No mapping yet.
	roles, err := repo.RolesFor(ctx, g1, "ping")
	require.NoError(t, err)
	assert.Empty(t, roles)

	// SetRoles establishes the exact role set for a command.
	require.NoError(t, repo.SetRoles(ctx, g1, "ping", []string{"r1", "r2"}, "u1"))
	roles, err = repo.RolesFor(ctx, g1, "ping")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"r1", "r2"}, roles)

	// The granting user and an app-supplied timestamp are persisted.
	var createdBy string
	var createdAt time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT created_by_user_id, created_at FROM permissions
		 WHERE servers_id = $1 AND command = $2 AND role_id = $3`, g1, "ping", "r1").
		Scan(&createdBy, &createdAt))
	assert.Equal(t, "u1", createdBy)
	assert.False(t, createdAt.IsZero(), "created_at is supplied by the app, not left null")

	// SetRoles diffs against the current set: it adds new roles and drops absent
	// ones in one call (here drop r1, keep r2, add r3).
	require.NoError(t, repo.SetRoles(ctx, g1, "ping", []string{"r2", "r3"}, "u2"))
	roles, err = repo.RolesFor(ctx, g1, "ping")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"r2", "r3"}, roles)

	// A kept role's original grant metadata is preserved (not re-inserted).
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT created_by_user_id FROM permissions
		 WHERE servers_id = $1 AND command = $2 AND role_id = $3`, g1, "ping", "r2").
		Scan(&createdBy))
	assert.Equal(t, "u1", createdBy, "r2 was kept, so its original creator stands")

	// Isolation: another server sees nothing for the same command.
	roles, err = repo.RolesFor(ctx, g2, "ping")
	require.NoError(t, err)
	assert.Empty(t, roles, "mappings are per server")

	// List returns every mapping on the server.
	require.NoError(t, repo.SetRoles(ctx, g1, "permissions", []string{"r9"}, "u1"))
	all, err := repo.List(ctx, g1)
	require.NoError(t, err)
	assert.ElementsMatch(t, []Mapping{
		{Command: "ping", RoleID: "r2"},
		{Command: "ping", RoleID: "r3"},
		{Command: "permissions", RoleID: "r9"},
	}, all)

	// SetRoles with an empty set clears a command, leaving others intact.
	require.NoError(t, repo.SetRoles(ctx, g1, "ping", nil, "u1"))
	roles, err = repo.RolesFor(ctx, g1, "ping")
	require.NoError(t, err)
	assert.Empty(t, roles)
	all, err = repo.List(ctx, g1)
	require.NoError(t, err)
	assert.Equal(t, []Mapping{{Command: "permissions", RoleID: "r9"}}, all)
}
