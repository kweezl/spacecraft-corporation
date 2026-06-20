package migrator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/migrator"
	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

func TestRun_CreatesTables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// An empty (un-migrated) database: this test exercises migrator.Run itself.
	pool := testdb.NewEmptyDB(t)

	require.NoError(t, migrator.Run(pool, zap.NewNop()))

	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_name IN ('servers', 'server_event')`).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}
