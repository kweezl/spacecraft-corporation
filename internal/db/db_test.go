package db_test

import (
	"context"
	"net"
	"net/url"
	"os"
	"strings"
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
	// The module builds its DSN from POSTGRES_* now, so decompose the test DSN
	// into those parts rather than passing a whole connection string.
	setPostgresEnvFromDSN(t, dsn)

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

func setPostgresEnvFromDSN(t *testing.T, dsn string) {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)

	host, port, err := net.SplitHostPort(u.Host)
	if err != nil { // no explicit port
		host, port = u.Host, "5432"
	}
	pw, _ := u.User.Password()
	sslmode := u.Query().Get("sslmode")
	if sslmode == "" {
		sslmode = "disable"
	}

	t.Setenv("POSTGRES_HOST", host)
	t.Setenv("POSTGRES_PORT", port)
	t.Setenv("POSTGRES_USER", u.User.Username())
	t.Setenv("POSTGRES_PASSWORD", pw)
	t.Setenv("POSTGRES_DB", strings.TrimPrefix(u.Path, "/"))
	t.Setenv("POSTGRES_SSLMODE", sslmode)
}
