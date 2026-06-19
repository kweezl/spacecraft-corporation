package migrator_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/migrator"
	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

func TestRun_CreatesTables(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	// Clean (not Reset): this test exercises migrator.Run itself.
	pool := testdb.Clean(t, dsn)

	require.NoError(t, migrator.Run(pool, zap.NewNop()))

	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_name IN ('ping_log', 'servers', 'server_event')`).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
}
