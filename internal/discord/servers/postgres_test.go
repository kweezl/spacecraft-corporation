package servers

import (
	"context"
	"os"
	"testing"

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

	repo := newRepository(pool)

	// First sighting of an allowlisted server: inserted + approved.
	isNew, err := repo.Upsert(ctx, "g1", "First", true)
	require.NoError(t, err)
	assert.True(t, isNew, "first upsert should insert")

	approved, err := repo.IsApproved(ctx, "g1")
	require.NoError(t, err)
	assert.True(t, approved)

	// Re-seeing it is an update, not an insert.
	isNew, err = repo.Upsert(ctx, "g1", "Renamed", true)
	require.NoError(t, err)
	assert.False(t, isNew, "second upsert should update")

	// An unlisted server starts unapproved...
	isNew, err = repo.Upsert(ctx, "g2", "Other", false)
	require.NoError(t, err)
	assert.True(t, isNew)
	approved, err = repo.IsApproved(ctx, "g2")
	require.NoError(t, err)
	assert.False(t, approved)

	// ...and the allowlist can only promote, never demote: a manual approval
	// survives a later upsert with inList=false.
	_, err = pool.Exec(ctx, `UPDATE servers SET approved = true WHERE server_id = $1`, "g2")
	require.NoError(t, err)
	_, err = repo.Upsert(ctx, "g2", "Other", false)
	require.NoError(t, err)
	approved, err = repo.IsApproved(ctx, "g2")
	require.NoError(t, err)
	assert.True(t, approved, "upsert with inList=false must not demote an approved server")

	// Unknown server is not approved.
	approved, err = repo.IsApproved(ctx, "nope")
	require.NoError(t, err)
	assert.False(t, approved)

	// Event log.
	require.NoError(t, repo.LogEvent(ctx, "g1", EventJoined))
	require.NoError(t, repo.LogEvent(ctx, "g1", EventRemoved))
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM server_event WHERE server_id = $1`, "g1").Scan(&n))
	assert.Equal(t, 2, n)
}
