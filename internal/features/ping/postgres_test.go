package ping

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kweezl/spacecraft-cadet/internal/migrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPgRepository_RecordAndCount(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS ping_log, goose_db_version`)
	require.NoError(t, migrator.Run(pool, zap.NewNop()))

	repo := newRepository(pool)
	require.NoError(t, repo.Record(ctx, "s1", "u1"))
	require.NoError(t, repo.Record(ctx, "s1", "u2"))
	require.NoError(t, repo.Record(ctx, "s2", "u1"))

	n, err := repo.Count(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}
