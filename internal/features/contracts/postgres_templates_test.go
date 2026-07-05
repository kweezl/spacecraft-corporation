package contracts

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/kweezl/spacecraft-corporation/internal/outbox"
	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

type templatesSuite struct {
	testdb.Suite
	tpls TemplateRepository
	repo Repository
}

func (s *templatesSuite) SetupSuite() {
	s.Suite.SetupSuite()
	s.tpls = newTemplateRepository(s.Pool, outbox.NewEnqueuer())
	s.repo = newRepository(s.Pool, outbox.NewEnqueuer())
}

func TestPgTemplateRepository(t *testing.T) { suite.Run(t, new(templatesSuite)) }

func (s *templatesSuite) seed() (TemplateRepository, context.Context, uuid.UUID) {
	g1 := testdb.SeedServer(s.T(), s.Pool, "g1")
	return s.tpls, context.Background(), g1
}

func (s *templatesSuite) TestCreateAndRoundTrip() {
	t := s.T()
	tpls, ctx, g := s.seed()

	id, err := tpls.CreateTemplate(ctx, g, "Weekly Steel", "Restock the mills", decimal.Zero, mgr)
	require.NoError(t, err)

	got, err := tpls.TemplateByID(ctx, g, id)
	require.NoError(t, err)
	assert.Equal(t, "Weekly Steel", got.Title)
	assert.Equal(t, "Restock the mills", got.Description)
	assert.True(t, got.RewardCredits.IsZero(), "zero-value default, got %s", got.RewardCredits)
	assert.True(t, got.ParticipantRewardFactor.IsZero(), "the passed factor persists, got %s", got.ParticipantRewardFactor)
	assert.Zero(t, got.RewardReputation)
	assert.Zero(t, got.RewardLicencePoints)
	assert.Zero(t, got.DeadlineMinutes)
	assert.Empty(t, got.LocationGDID)
	assert.Equal(t, mgr, got.CreatedByUserID)
	assert.Empty(t, got.Items)

	// Details: title, description, deadline duration.
	require.NoError(t, tpls.UpdateTemplateDetails(ctx, g, id, "Weekly Steel v2", "d2", 3*24*60, mgr))
	// Rewards keep decimal fidelity (12.50 round-trips exactly, never a float).
	require.NoError(t, tpls.UpdateTemplateRewards(ctx, g, id, dec("12.50"), dec("25"), 7, 3, mgr))
	// Location pair.
	require.NoError(t, tpls.SetTemplateLocation(ctx, g, id, "Station_Cairn", "v1", mgr))

	got, err = tpls.TemplateByID(ctx, g, id)
	require.NoError(t, err)
	assert.Equal(t, "Weekly Steel v2", got.Title)
	assert.Equal(t, 3*24*60, got.DeadlineMinutes)
	assert.True(t, got.RewardCredits.Equal(dec("12.50")), "got %s", got.RewardCredits)
	assert.True(t, got.ParticipantRewardFactor.Equal(dec("25")), "got %s", got.ParticipantRewardFactor)
	assert.Equal(t, 7, got.RewardReputation)
	assert.Equal(t, 3, got.RewardLicencePoints)
	assert.Equal(t, "Station_Cairn", got.LocationGDID)
	assert.Equal(t, "v1", got.LocationGDVersion)

	// Clearing the location with an empty gdid drops the version too.
	require.NoError(t, tpls.SetTemplateLocation(ctx, g, id, "", "v1", mgr))
	got, err = tpls.TemplateByID(ctx, g, id)
	require.NoError(t, err)
	assert.Empty(t, got.LocationGDID)
	assert.Empty(t, got.LocationGDVersion)
}

// TestCreateWithFactorPrefill persists the factor passed at creation (the
// caller prefills it from the server default).
func (s *templatesSuite) TestCreateWithFactorPrefill() {
	t := s.T()
	tpls, ctx, g := s.seed()

	id, err := tpls.CreateTemplate(ctx, g, "Prefilled", "", dec("12.5"), mgr)
	require.NoError(t, err)
	got, err := tpls.TemplateByID(ctx, g, id)
	require.NoError(t, err)
	assert.True(t, got.ParticipantRewardFactor.Equal(dec("12.5")), "got %s", got.ParticipantRewardFactor)
}

func (s *templatesSuite) TestTitleUniqueness() {
	t := s.T()
	tpls, ctx, g := s.seed()

	a, err := tpls.CreateTemplate(ctx, g, "Steel Run", "", decimal.Zero, mgr)
	require.NoError(t, err)
	_, err = tpls.CreateTemplate(ctx, g, "steel run", "", decimal.Zero, mgr)
	require.ErrorIs(t, err, ErrTemplateExists, "case-insensitive title collision")

	// Renaming onto an existing title collides too; renaming onto itself is fine.
	b, err := tpls.CreateTemplate(ctx, g, "Copper Run", "", decimal.Zero, mgr)
	require.NoError(t, err)
	require.ErrorIs(t, tpls.UpdateTemplateDetails(ctx, g, b, "STEEL RUN", "", 0, mgr), ErrTemplateExists)
	require.NoError(t, tpls.UpdateTemplateDetails(ctx, g, a, "Steel Run", "same title", 0, mgr))

	// Another server may reuse the title.
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	_, err = tpls.CreateTemplate(ctx, g2, "Steel Run", "", decimal.Zero, mgr)
	require.NoError(t, err)
}

func (s *templatesSuite) TestItems_AddUpdateRemove() {
	t := s.T()
	tpls, ctx, g := s.seed()
	id, err := tpls.CreateTemplate(ctx, g, "T", "", decimal.Zero, mgr)
	require.NoError(t, err)

	require.NoError(t, tpls.AddTemplateItem(ctx, g, id, "SteelIngot", "v1", 500, 2, mgr))
	require.ErrorIs(t, tpls.AddTemplateItem(ctx, g, id, "SteelIngot", "v1", 10, 2, mgr), ErrTemplateItemExists)
	require.NoError(t, tpls.AddTemplateItem(ctx, g, id, "CopperWire", "v1", 100, 2, mgr))
	require.ErrorIs(t, tpls.AddTemplateItem(ctx, g, id, "IronPlate", "v1", 10, 2, mgr), ErrMaxItems)

	got, err := tpls.TemplateByID(ctx, g, id)
	require.NoError(t, err)
	require.Len(t, got.Items, 2)
	assert.Equal(t, "SteelIngot", got.Items[0].GDID)
	assert.Equal(t, "v1", got.Items[0].GDVersion)
	assert.Equal(t, 500, got.Items[0].Qty)

	tid, err := tpls.UpdateTemplateItemQty(ctx, g, got.Items[0].ID, 750, mgr)
	require.NoError(t, err)
	assert.Equal(t, id, tid)

	tid, err = tpls.RemoveTemplateItem(ctx, g, got.Items[1].ID, mgr)
	require.NoError(t, err)
	assert.Equal(t, id, tid)

	got, err = tpls.TemplateByID(ctx, g, id)
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	assert.Equal(t, 750, got.Items[0].Qty)

	// Forged/cross-server item ids resolve to nothing.
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	_, err = tpls.UpdateTemplateItemQty(ctx, g2, got.Items[0].ID, 1, mgr)
	require.ErrorIs(t, err, ErrTemplateItemNotFound)
	_, err = tpls.RemoveTemplateItem(ctx, g, uuid.New(), mgr)
	require.ErrorIs(t, err, ErrTemplateItemNotFound)
}

func (s *templatesSuite) TestListTemplates_SearchAndPaging() {
	t := s.T()
	tpls, ctx, g := s.seed()
	for _, title := range []string{"Steel Run", "Steel Rush", "Copper Run", "100% Special_Char"} {
		_, err := tpls.CreateTemplate(ctx, g, title, "", decimal.Zero, mgr)
		require.NoError(t, err)
	}
	// Another server's templates never leak in.
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	_, err := tpls.CreateTemplate(ctx, g2, "Steel Elsewhere", "", decimal.Zero, mgr)
	require.NoError(t, err)

	// No query = all, ordered by title, paged.
	page, total, err := tpls.ListTemplates(ctx, g, "", 2, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	require.Len(t, page, 2)
	assert.Equal(t, "100% Special_Char", page[0].Title)
	assert.Equal(t, "Copper Run", page[1].Title)
	page, _, err = tpls.ListTemplates(ctx, g, "", 2, 2)
	require.NoError(t, err)
	require.Len(t, page, 2)
	assert.Equal(t, "Steel Run", page[0].Title)

	// Case-insensitive substring filter.
	page, total, err = tpls.ListTemplates(ctx, g, "steel", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, page, 2)

	// ILIKE metacharacters match literally, not as wildcards.
	_, total, err = tpls.ListTemplates(ctx, g, "100%", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	_, total, err = tpls.ListTemplates(ctx, g, "l_", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total, `"l_" matches only the literal "l_" in "Special_Char"`)

	// No match.
	page, total, err = tpls.ListTemplates(ctx, g, "zzz", 10, 0)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, page)
}

func (s *templatesSuite) TestDeleteTemplate_ContractSurvives() {
	t := s.T()
	tpls, ctx, g := s.seed()
	id, err := tpls.CreateTemplate(ctx, g, "Doomed", "", decimal.Zero, mgr)
	require.NoError(t, err)
	require.NoError(t, tpls.AddTemplateItem(ctx, g, id, "SteelIngot", "v1", 500, 25, mgr))

	// Instantiate a contract from it (values copied, provenance FK set).
	cid, err := s.repo.Create(ctx, CreateInput{
		ServerID: g, Kind: KindTemplate, Title: "From Doomed", TemplateID: &id,
		Items:           []CreateItemInput{{Name: "Steel Ingot", GDID: "SteelIngot", GDVersion: "v1", Qty: 500}},
		CreatedByUserID: mgr,
	})
	require.NoError(t, err)

	require.NoError(t, tpls.DeleteTemplate(ctx, g, id, mgr))
	_, err = tpls.TemplateByID(ctx, g, id)
	require.ErrorIs(t, err, ErrTemplateNotFound)

	// The contract keeps its copied values; only the provenance pointer nulls
	// (ON DELETE SET NULL — the deliberate RESTRICT exception).
	p, err := s.repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	assert.Nil(t, p.TemplateID)
	require.Len(t, p.Items, 1)
	assert.Equal(t, "SteelIngot", p.Items[0].GDID)
}

func (s *templatesSuite) TestTemplateLinkIsSameServerOnly() {
	t := s.T()
	tpls, ctx, g := s.seed()
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	id, err := tpls.CreateTemplate(ctx, g, "Mine", "", decimal.Zero, mgr)
	require.NoError(t, err)

	// A contract in another server cannot link this server's template — the
	// composite (contract_templates_id, servers_id) FK rejects it in the DB, even
	// though the handler flows never construct such an input.
	_, err = s.repo.Create(ctx, CreateInput{
		ServerID: g2, Kind: KindTemplate, Title: "Forged", TemplateID: &id, CreatedByUserID: mgr,
	})
	require.Error(t, err, "cross-server template link must be rejected")

	// The same input in the owning server is fine.
	_, err = s.repo.Create(ctx, CreateInput{
		ServerID: g, Kind: KindTemplate, Title: "Legit", TemplateID: &id, CreatedByUserID: mgr,
	})
	require.NoError(t, err)
}

func (s *templatesSuite) TestServerScoping() {
	t := s.T()
	tpls, ctx, g := s.seed()
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	id, err := tpls.CreateTemplate(ctx, g, "Mine", "", decimal.Zero, mgr)
	require.NoError(t, err)

	_, err = tpls.TemplateByID(ctx, g2, id)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	require.ErrorIs(t, tpls.UpdateTemplateDetails(ctx, g2, id, "Hax", "", 0, mgr), ErrTemplateNotFound)
	require.ErrorIs(t, tpls.UpdateTemplateRewards(ctx, g2, id, dec("1"), decimal.Zero, 0, 0, mgr), ErrTemplateNotFound)
	require.ErrorIs(t, tpls.SetTemplateLocation(ctx, g2, id, "X", "v1", mgr), ErrTemplateNotFound)
	require.ErrorIs(t, tpls.AddTemplateItem(ctx, g2, id, "X", "v1", 1, 25, mgr), ErrTemplateNotFound)
	require.ErrorIs(t, tpls.DeleteTemplate(ctx, g2, id, mgr), ErrTemplateNotFound)
}
