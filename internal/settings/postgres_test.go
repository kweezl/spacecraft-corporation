package settings

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
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

// TestSupplyAndMaxItemsColumns round-trips the supply forum / request limit and
// the contracts max-items columns: NULL vs value for the *int columns, and that
// each setter is column-independent.
func (s *pgSuite) TestSupplyAndMaxItemsColumns() {
	t := s.T()
	ctx := context.Background()
	g1 := testdb.SeedServer(t, s.Pool, "g1")
	repo := newRepository(s.Pool)

	// Unset: empty forum, nil pointers.
	got, err := repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.Empty(t, got.SupplyForumChannelID)
	assert.Nil(t, got.SupplyRequestLimit)
	assert.Nil(t, got.ContractsMaxItems)
	assert.False(t, got.ContractsReportCSV, "payout CSV defaults to off")

	// Set each; all persist independently.
	require.NoError(t, repo.SetSupplyForumChannelID(ctx, g1, "supply-forum"))
	require.NoError(t, repo.SetSupplyRequestLimit(ctx, g1, 15))
	require.NoError(t, repo.SetContractsMaxItems(ctx, g1, 40))
	require.NoError(t, repo.SetContractsReportCSV(ctx, g1, true))
	got, err = repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.Equal(t, "supply-forum", got.SupplyForumChannelID)
	require.NotNil(t, got.SupplyRequestLimit)
	assert.Equal(t, 15, *got.SupplyRequestLimit)
	require.NotNil(t, got.ContractsMaxItems)
	assert.Equal(t, 40, *got.ContractsMaxItems)
	assert.True(t, got.ContractsReportCSV)

	// Independent upsert: changing the theme preserves the new columns.
	require.NoError(t, repo.SetTheme(ctx, g1, "lore"))
	got, err = repo.Get(ctx, g1)
	require.NoError(t, err)
	assert.Equal(t, "supply-forum", got.SupplyForumChannelID)
	require.NotNil(t, got.SupplyRequestLimit)
	assert.Equal(t, 15, *got.SupplyRequestLimit)

	// The CHECK (> 0) rejects a non-positive limit.
	require.Error(t, repo.SetSupplyRequestLimit(ctx, g1, 0))
	require.Error(t, repo.SetContractsMaxItems(ctx, g1, -1))
}

// TestNewGettersCache exercises the Store's cached getters for the three new
// columns: a miss reads unset, a set invalidates and re-reads the value.
func (s *pgSuite) TestNewGettersCache() {
	t := s.T()
	ctx := context.Background()
	g1 := testdb.SeedServer(t, s.Pool, "g1")
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	store, err := NewStore(newRepository(s.Pool), tr, zap.NewNop())
	require.NoError(t, err)

	// Miss: unset.
	if _, ok := store.SupplyForumChannelID(ctx, g1); ok {
		t.Fatal("supply forum should be unset")
	}
	if _, ok := store.SupplyRequestLimit(ctx, g1); ok {
		t.Fatal("supply limit should be unset")
	}
	if _, ok := store.ContractsMaxItems(ctx, g1); ok {
		t.Fatal("contracts max-items should be unset")
	}
	assert.False(t, store.ContractsReportCSV(ctx, g1), "payout CSV defaults to off")

	// Set + invalidate → cached getters see the new values.
	require.NoError(t, store.SetSupplyForumChannelID(ctx, g1, "sf"))
	require.NoError(t, store.SetSupplyRequestLimit(ctx, g1, 7))
	require.NoError(t, store.SetContractsMaxItems(ctx, g1, 30))
	require.NoError(t, store.SetContractsReportCSV(ctx, g1, true))

	forum, ok := store.SupplyForumChannelID(ctx, g1)
	assert.True(t, ok)
	assert.Equal(t, "sf", forum)
	limit, ok := store.SupplyRequestLimit(ctx, g1)
	assert.True(t, ok)
	assert.Equal(t, 7, limit)
	maxItems, ok := store.ContractsMaxItems(ctx, g1)
	assert.True(t, ok)
	assert.Equal(t, 30, maxItems)
	assert.True(t, store.ContractsReportCSV(ctx, g1))
}
