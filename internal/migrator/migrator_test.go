package migrator_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/migrator"
)

func TestRun_CreatesTables(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// Clean slate so the test is repeatable.
	_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS ping_log, goose_db_version`)

	require.NoError(t, migrator.Run(pool, zap.NewNop()))

	var n int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_name = 'ping_log'`).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}
