package settings

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
	// server_settings.servers_id references servers.id; SeedServer returns that id.
	g1 := testdb.SeedServer(t, pool, "g1")
	g2 := testdb.SeedServer(t, pool, "g2")
	repo := newRepository(pool)

	// Unknown server: zero settings, no error.
	got, err := repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.Equal(t, Settings{}, got)

	// Set theme; language stays unset.
	require.NoError(t, repo.SetTheme(ctx, g1, "lore"))
	got, err = repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.Equal(t, Settings{Theme: "lore"}, got)

	// Set language; theme is preserved (independent column upsert).
	require.NoError(t, repo.SetLanguage(ctx, g1, "ru"))
	got, err = repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.Equal(t, Settings{Theme: "lore", Language: "ru"}, got)

	// Update theme; language still preserved.
	require.NoError(t, repo.SetTheme(ctx, g1, "standard"))
	got, err = repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.Equal(t, Settings{Theme: "standard", Language: "ru"}, got)

	// Isolation: another server is independent.
	got, err = repo.Get(ctx, g2)
	require.NoError(t, err)
	assert.Equal(t, Settings{}, got)
}
