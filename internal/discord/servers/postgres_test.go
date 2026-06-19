package servers

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
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

	// First sighting of an allowlisted server: inserted + approved, returning its id.
	id1, approved, isNew, err := repo.Upsert(ctx, "g1", "First", true)
	require.NoError(t, err)
	assert.True(t, isNew, "first upsert should insert")
	assert.True(t, approved)
	assert.NotEqual(t, uuid.Nil, id1, "upsert returns the new row's id")

	gotID, gotApproved, found, err := repo.Get(ctx, "g1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.True(t, gotApproved)
	assert.Equal(t, id1, gotID, "Get returns the same id as the insert")

	// Re-seeing it is an update, not an insert, and the id is stable.
	id1Again, _, isNew, err := repo.Upsert(ctx, "g1", "Renamed", true)
	require.NoError(t, err)
	assert.False(t, isNew, "second upsert should update")
	assert.Equal(t, id1, id1Again, "the id is preserved across updates")

	// An unlisted server starts unapproved...
	_, approved, isNew, err = repo.Upsert(ctx, "g2", "Other", false)
	require.NoError(t, err)
	assert.True(t, isNew)
	assert.False(t, approved)

	// ...and the allowlist can only promote, never demote: a manual approval
	// survives a later upsert with inList=false.
	_, err = pool.Exec(ctx, `UPDATE servers SET approved = true WHERE server_id = $1`, "g2")
	require.NoError(t, err)
	_, approved, _, err = repo.Upsert(ctx, "g2", "Other", false)
	require.NoError(t, err)
	assert.True(t, approved, "upsert with inList=false must not demote an approved server")

	// Unknown server is reported as not found.
	_, _, found, err = repo.Get(ctx, "nope")
	require.NoError(t, err)
	assert.False(t, found)

	// Event log.
	require.NoError(t, repo.LogEvent(ctx, "g1", EventJoined))
	require.NoError(t, repo.LogEvent(ctx, "g1", EventRemoved))
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM server_event WHERE server_id = $1`, "g1").Scan(&n))
	assert.Equal(t, 2, n)
}
