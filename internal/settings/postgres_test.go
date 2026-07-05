package settings

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

type pgSuite struct{ testdb.Suite }

func TestPgRepository(t *testing.T) { suite.Run(t, new(pgSuite)) }

func (s *pgSuite) TestRepository() {
	t := s.T()
	ctx := context.Background()
	pool := s.Pool
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

// TestRewardFactor round-trips the contracts participant reward factor. Decimal
// comparisons use Equal (representation differs between a zero value and a
// scanned NUMERIC 0), so this is a separate test from the struct-equality one.
func (s *pgSuite) TestRewardFactor() {
	t := s.T()
	ctx := context.Background()
	g1 := testdb.SeedServer(t, s.Pool, "g1")
	repo := newRepository(s.Pool)

	// Unset (no row): reads as zero.
	got, err := repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.True(t, got.ContractsRewardFactor.IsZero())

	// Set with a fraction; other columns stay unset.
	require.NoError(t, repo.SetContractsRewardFactor(ctx, g1, decimal.RequireFromString("12.50")))
	got, err = repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.True(t, got.ContractsRewardFactor.Equal(decimal.RequireFromString("12.5")), "got %s", got.ContractsRewardFactor)
	assert.Empty(t, got.Theme)

	// Independent column upsert: setting the theme preserves the factor.
	require.NoError(t, repo.SetTheme(ctx, g1, "lore"))
	got, err = repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.True(t, got.ContractsRewardFactor.Equal(decimal.RequireFromString("12.5")))
	assert.Equal(t, "lore", got.Theme)

	// Overwrite back to zero.
	require.NoError(t, repo.SetContractsRewardFactor(ctx, g1, decimal.Zero))
	got, err = repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.True(t, got.ContractsRewardFactor.IsZero())
}

// TestReportsChannel round-trips the contracts reports channel and shows the
// upsert is column-independent (setting it preserves the forum channel).
func (s *pgSuite) TestReportsChannel() {
	t := s.T()
	ctx := context.Background()
	g1 := testdb.SeedServer(t, s.Pool, "g1")
	repo := newRepository(s.Pool)

	// Unset (no row): reads as empty.
	got, err := repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.Empty(t, got.ContractsReportsChannelID)

	// Set alongside an existing forum channel; both persist independently.
	require.NoError(t, repo.SetContractsForumChannelID(ctx, g1, "forum-1"))
	require.NoError(t, repo.SetContractsReportsChannelID(ctx, g1, "reports-1"))
	got, err = repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.Equal(t, "forum-1", got.ContractsForumChannelID)
	assert.Equal(t, "reports-1", got.ContractsReportsChannelID)
}
