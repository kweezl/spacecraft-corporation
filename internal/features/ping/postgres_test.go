package ping

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

func TestPgRepository_RecordAndCount(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool := testdb.Reset(t, dsn)
	// ping_log.servers_id references servers.id, so the servers rows must exist;
	// SeedServer returns the servers.id the repository now keys on.
	s1 := testdb.SeedServer(t, pool, "s1")
	s2 := testdb.SeedServer(t, pool, "s2")

	repo := newRepository(pool)
	require.NoError(t, repo.Record(ctx, s1, "u1"))
	require.NoError(t, repo.Record(ctx, s1, "u2"))
	require.NoError(t, repo.Record(ctx, s2, "u1"))

	n, err := repo.Count(ctx, s1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}
