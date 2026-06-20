package bases

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

const (
	u1 = "user-1"
	u2 = "user-2"
)

type basesSuite struct {
	testdb.Suite
	repo Repository
}

func (s *basesSuite) SetupSuite() {
	s.Suite.SetupSuite()
	s.repo = newRepository(s.Pool)
}

func TestPgRepository(t *testing.T) { suite.Run(t, new(basesSuite)) }

// seed returns the repo plus two freshly-seeded servers; SetupTest truncated the
// tables first, so each test starts clean.
func (s *basesSuite) seed() (Repository, context.Context, uuid.UUID, uuid.UUID) {
	g1 := testdb.SeedServer(s.T(), s.Pool, "g1")
	g2 := testdb.SeedServer(s.T(), s.Pool, "g2")
	return s.repo, context.Background(), g1, g2
}

func memberBase(server uuid.UUID, owner, name string) RegisterInput {
	return RegisterInput{
		ServerID: server, Kind: KindMember, OwnerUserID: owner, CreatedByUserID: owner,
		Name: name, SectorName: "Orion", SystemCode: "SOL", PlanetNumber: 3,
	}
}

func corpBase(server uuid.UUID, by, name string) RegisterInput {
	return RegisterInput{
		ServerID: server, Kind: KindCorp, CreatedByUserID: by,
		Name: name, SectorName: "Orion", SystemCode: "SOL", PlanetNumber: 4,
	}
}

func (s *basesSuite) TestRegister_AndMemberLimit() {
	t := s.T()
	repo, ctx, g1, _ := s.seed()

	for i, name := range []string{"Alpha", "Beta", "Gamma"} {
		_, err := repo.Register(ctx, memberBase(g1, u1, name), 3)
		require.NoErrorf(t, err, "register #%d", i)
	}
	// Fourth exceeds the member limit.
	_, err := repo.Register(ctx, memberBase(g1, u1, "Delta"), 3)
	require.ErrorIs(t, err, ErrLimitReached)

	// A different member has their own quota.
	_, err = repo.Register(ctx, memberBase(g1, u2, "Echo"), 3)
	require.NoError(t, err)

	// A soft-deleted base frees a slot.
	owned, err := repo.ListOwned(ctx, MemberOwnership(g1, u1), 25)
	require.NoError(t, err)
	require.Len(t, owned, 3)
	n, err := repo.DeleteOne(ctx, MemberOwnership(g1, u1), owned[0].ID)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, err = repo.Register(ctx, memberBase(g1, u1, "Delta"), 3)
	require.NoError(t, err, "deleting a base frees a member slot")
}

func (s *basesSuite) TestCorpLimit_AndKindIsolation() {
	t := s.T()
	repo, ctx, g1, _ := s.seed()

	_, err := repo.Register(ctx, corpBase(g1, u1, "Corp-A"), 1)
	require.NoError(t, err)
	_, err = repo.Register(ctx, corpBase(g1, u1, "Corp-B"), 1)
	require.ErrorIs(t, err, ErrLimitReached, "corp limit is independent of member bases")

	// A member-tier scope never sees corp bases (kind mismatch).
	owned, err := repo.ListOwned(ctx, MemberOwnership(g1, u1), 25)
	require.NoError(t, err)
	assert.Empty(t, owned, "corp bases are not member-owned")

	corp, err := repo.ListOwned(ctx, CorpOwnership(g1), 25)
	require.NoError(t, err)
	require.Len(t, corp, 1)
}

func (s *basesSuite) TestOwnershipIsolation_Mutations() {
	t := s.T()
	repo, ctx, g1, g2 := s.seed()

	id, err := repo.Register(ctx, memberBase(g1, u1, "Alpha"), 3)
	require.NoError(t, err)

	// u2 cannot soft-delete u1's base — predicate matches nothing.
	n, err := repo.DeleteOne(ctx, MemberOwnership(g1, u2), id)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "another member cannot delete a base they don't own")

	// The corp tier cannot delete a member base either.
	n, err = repo.DeleteOne(ctx, CorpOwnership(g1), id)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// A different server cannot reach it.
	n, err = repo.DeleteOne(ctx, MemberOwnership(g2, u1), id)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// u2 cannot install equipment on u1's base.
	err = repo.AddExtractor(ctx, MemberOwnership(g1, u2), id, "Iron", 4)
	require.ErrorIs(t, err, ErrBaseNotFound)

	// u1 can, and u2 cannot see it.
	require.NoError(t, repo.AddExtractor(ctx, MemberOwnership(g1, u1), id, "Iron", 4))
	ex, err := repo.ListExtractors(ctx, MemberOwnership(g1, u2), id)
	require.NoError(t, err)
	assert.Empty(t, ex, "equipment of a base you don't own is not visible")

	ex, err = repo.ListExtractors(ctx, MemberOwnership(g1, u1), id)
	require.NoError(t, err)
	require.Len(t, ex, 1)

	// u2 cannot remove u1's extractor.
	removed, err := repo.RemoveExtractor(ctx, MemberOwnership(g1, u2), ex[0].ID)
	require.NoError(t, err)
	assert.Equal(t, 0, removed)
	removed, err = repo.RemoveExtractor(ctx, MemberOwnership(g1, u1), ex[0].ID)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
}

func (s *basesSuite) TestEquipmentLimits() {
	t := s.T()
	repo, ctx, g1, _ := s.seed()
	id, err := repo.Register(ctx, memberBase(g1, u1, "Alpha"), 3)
	require.NoError(t, err)
	o := MemberOwnership(g1, u1)

	for range 2 {
		require.NoError(t, repo.AddExtractor(ctx, o, id, "Iron", 2))
	}
	require.ErrorIs(t, repo.AddExtractor(ctx, o, id, "Iron", 2), ErrLimitReached)

	require.NoError(t, repo.AddProduction(ctx, o, id, "Plate", 1))
	require.ErrorIs(t, repo.AddProduction(ctx, o, id, "Gear", 1), ErrLimitReached)
}

func (s *basesSuite) TestDeleteAll() {
	t := s.T()
	repo, ctx, g1, _ := s.seed()
	for _, n := range []string{"A", "B", "C"} {
		_, err := repo.Register(ctx, memberBase(g1, u1, n), 5)
		require.NoError(t, err)
	}
	_, err := repo.Register(ctx, memberBase(g1, u2, "Z"), 5)
	require.NoError(t, err)

	n, err := repo.DeleteAll(ctx, MemberOwnership(g1, u1))
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	owned, err := repo.ListOwned(ctx, MemberOwnership(g1, u1), 25)
	require.NoError(t, err)
	assert.Empty(t, owned)
	// u2 is untouched.
	owned, err = repo.ListOwned(ctx, MemberOwnership(g1, u2), 25)
	require.NoError(t, err)
	assert.Len(t, owned, 1)
}

func (s *basesSuite) TestList_FiltersAndPagination() {
	t := s.T()
	repo, ctx, g1, g2 := s.seed()

	// One base on g2 must never appear in g1 listings.
	_, err := repo.Register(ctx, memberBase(g2, u1, "Foreign"), 5)
	require.NoError(t, err)

	mk := func(name, sector, system string, planet int) uuid.UUID {
		in := memberBase(g1, u1, name)
		in.SectorName, in.SystemCode, in.PlanetNumber = sector, system, planet
		id, err := repo.Register(ctx, in, 99)
		require.NoError(t, err)
		return id
	}
	iron := mk("Ironworks", "Orion", "SOL", 3)
	mk("Gasworks", "Cygnus", "VEGA", 5)
	mk("Farm", "Orion", "SOL", 7)
	require.NoError(t, repo.AddExtractor(ctx, MemberOwnership(g1, u1), iron, "Iron Ore", 4))
	require.NoError(t, repo.AddProduction(ctx, MemberOwnership(g1, u1), iron, "Steel Plate", 30))

	// Unfiltered: server isolation + total.
	page, total, err := repo.List(ctx, g1, Filter{}, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, page, 3)

	// Sector filter (case-insensitive substring).
	page, total, err = repo.List(ctx, g1, Filter{SectorName: "ORI"}, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, page, 2)

	// Resource filter matches the base via its extractor, and equipment is attached.
	page, total, err = repo.List(ctx, g1, Filter{Resource: "iron"}, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, page, 1)
	assert.Equal(t, "Ironworks", page[0].Name)
	require.Len(t, page[0].Extractors, 1)
	assert.Equal(t, "Iron Ore", page[0].Extractors[0].ResourceName)
	require.Len(t, page[0].Productions, 1)
	assert.Equal(t, "Steel Plate", page[0].Productions[0].ItemName)

	// Item filter.
	_, total, err = repo.List(ctx, g1, Filter{Item: "steel"}, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total)

	// Pagination: page size 2 over 3 results.
	p1, total, err := repo.List(ctx, g1, Filter{}, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	require.Len(t, p1, 2)
	p2, _, err := repo.List(ctx, g1, Filter{}, 2, 2)
	require.NoError(t, err)
	require.Len(t, p2, 1)
	// No overlap across pages (ordered by name, id).
	assert.NotEqual(t, p1[0].ID, p2[0].ID)
}
