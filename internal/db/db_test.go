package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/kweezl/spacecraft-corporation/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
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
		fx.Invoke(func(p interface{ Ping(context.Context) error }) {
			assert.NoError(t, p.Ping(context.Background()))
		}),
	)
	require.NoError(t, app.Start(context.Background()))
	app.RequireStop()
}
