package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/db"
)

func TestPool_ConnectsAndPings(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	t.Setenv("DATABASE_URL", dsn)

	app := fxtest.New(t,
		fx.Provide(func() *zap.Logger { return zap.NewNop() }),
		db.Module(),
		fx.Invoke(func(p *pgxpool.Pool) {
			assert.NoError(t, p.Ping(context.Background()))
		}),
	)
	require.NoError(t, app.Start(context.Background()))
	app.RequireStop()
}
