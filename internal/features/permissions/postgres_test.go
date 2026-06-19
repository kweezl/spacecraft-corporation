package permissions

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

func TestPgRepository(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool := testdb.Reset(t, dsn)
	// permissions.servers_id references servers.id; SeedServer returns that id.
	g1 := testdb.SeedServer(t, pool, "g1")
	g2 := testdb.SeedServer(t, pool, "g2")

	repo := newRepository(pool)

	// No mapping yet.
	roles, err := repo.RolesFor(ctx, g1, "ping")
	require.NoError(t, err)
	assert.Empty(t, roles)

	// Grant is idempotent: granting the same role twice keeps one row.
	require.NoError(t, repo.Grant(ctx, g1, "ping", "r1", "u1"))
	require.NoError(t, repo.Grant(ctx, g1, "ping", "r1", "u1"))
	require.NoError(t, repo.Grant(ctx, g1, "ping", "r2", "u1"))

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

	// Isolation: another server sees nothing for the same command.
	roles, err = repo.RolesFor(ctx, g2, "ping")
	require.NoError(t, err)
	assert.Empty(t, roles, "mappings are per server")

	// Revoke removes a single role.
	require.NoError(t, repo.Revoke(ctx, g1, "ping", "r1"))
	roles, err = repo.RolesFor(ctx, g1, "ping")
	require.NoError(t, err)
	assert.Equal(t, []string{"r2"}, roles)

	// List returns every mapping on the server.
	require.NoError(t, repo.Grant(ctx, g1, "permissions", "r9", "u1"))
	all, err := repo.List(ctx, g1)
	require.NoError(t, err)
	assert.ElementsMatch(t, []Mapping{
		{Command: "ping", RoleID: "r2"},
		{Command: "permissions", RoleID: "r9"},
	}, all)

	// Clear removes all roles for one command, leaving others intact.
	require.NoError(t, repo.Clear(ctx, g1, "ping"))
	roles, err = repo.RolesFor(ctx, g1, "ping")
	require.NoError(t, err)
	assert.Empty(t, roles)
	all, err = repo.List(ctx, g1)
	require.NoError(t, err)
	assert.Equal(t, []Mapping{{Command: "permissions", RoleID: "r9"}}, all)
}
