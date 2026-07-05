package supply

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/kweezl/spacecraft-corporation/internal/outbox"
	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

const (
	owner  = "owner-1"
	other  = "owner-2"
	member = "member-9"
	gdIron = "IronOre"
	gdCopr = "CopperOre"
)

type supplySuite struct {
	testdb.Suite
	repo Repository
}

func (s *supplySuite) SetupSuite() {
	s.Suite.SetupSuite()
	s.repo = newRepository(s.Pool, outbox.NewEnqueuer())
}

func TestPgRepository(t *testing.T) { suite.Run(t, new(supplySuite)) }

func (s *supplySuite) seed() (context.Context, uuid.UUID) {
	return context.Background(), testdb.SeedServer(s.T(), s.Pool, "g1")
}

// newRequest creates an open request and assigns it a thread (simulating the
// worker that normally posts the forum thread).
func (s *supplySuite) newRequest(ctx context.Context, g uuid.UUID, ownerID, thread string) uuid.UUID {
	rid, err := s.repo.Create(ctx, CreateInput{ServerID: g, OwnerUserID: ownerID, Title: "need parts", OpenLimit: 100})
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.repo.SetThreadID(ctx, rid, thread))
	return rid
}

func (s *supplySuite) taskCount(ctx context.Context, kind string, id uuid.UUID) int {
	var n int
	require.NoError(s.T(), s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_tasks WHERE kind = $1 AND chronometric_id = $2`, kind, id).Scan(&n))
	return n
}

func (s *supplySuite) status(ctx context.Context, id uuid.UUID) string {
	var st string
	require.NoError(s.T(), s.Pool.QueryRow(ctx, `SELECT status FROM supply_requests WHERE id = $1`, id).Scan(&st))
	return st
}

func (s *supplySuite) TestCreate_OutboxAndLimit() {
	t := s.T()
	ctx, g := s.seed()

	rid, err := s.repo.Create(ctx, CreateInput{ServerID: g, OwnerUserID: owner, Title: "t", OpenLimit: 2})
	require.NoError(t, err)
	assert.Equal(t, "open", s.status(ctx, rid))
	assert.Equal(t, 1, s.taskCount(ctx, taskCreate, rid), "create enqueues exactly one create task")

	// Second open request allowed (limit 2), third rejected.
	_, err = s.repo.Create(ctx, CreateInput{ServerID: g, OwnerUserID: owner, Title: "t2", OpenLimit: 2})
	require.NoError(t, err)
	_, err = s.repo.Create(ctx, CreateInput{ServerID: g, OwnerUserID: owner, Title: "t3", OpenLimit: 2})
	require.ErrorIs(t, err, ErrLimit)

	// Cancelling one frees a slot; cancelled/completed don't count toward the limit.
	require.NoError(t, s.repo.Cancel(ctx, g, owner, rid))
	_, err = s.repo.Create(ctx, CreateInput{ServerID: g, OwnerUserID: owner, Title: "t4", OpenLimit: 2})
	require.NoError(t, err)

	// Another owner is independent.
	_, err = s.repo.Create(ctx, CreateInput{ServerID: g, OwnerUserID: other, Title: "x", OpenLimit: 2})
	require.NoError(t, err)
}

func (s *supplySuite) TestOwnershipScoping() {
	t := s.T()
	ctx, g := s.seed()
	rid := s.newRequest(ctx, g, owner, "th-own")

	// Every owner-scoped mutation with the wrong owner affects zero rows.
	require.ErrorIs(t, s.repo.UpdateDetails(ctx, g, other, rid, "hijack", ""), ErrNotFound)
	require.ErrorIs(t, s.repo.Cancel(ctx, g, other, rid), ErrNotFound)
	require.ErrorIs(t, s.repo.SetDeliveryLocation(ctx, g, other, rid, "X", "v1"), ErrNotFound)
	require.ErrorIs(t, s.repo.AddItem(ctx, g, other, rid, gdIron, "v1", 5, maxItems), ErrNotFound)
	require.ErrorIs(t, s.repo.Republish(ctx, g, other, rid), ErrNotFound)

	// A forged request id also yields ErrNotFound.
	require.ErrorIs(t, s.repo.UpdateDetails(ctx, g, owner, uuid.New(), "x", ""), ErrNotFound)

	// The real owner still sees an unchanged title.
	prog, err := s.repo.ProgressByIDOwned(ctx, g, owner, rid)
	require.NoError(t, err)
	assert.Equal(t, "need parts", prog.Title)
}

func (s *supplySuite) TestItems() {
	t := s.T()
	ctx, g := s.seed()
	rid := s.newRequest(ctx, g, owner, "th-items")

	require.NoError(t, s.repo.AddItem(ctx, g, owner, rid, gdIron, "v1", 10, maxItems))
	require.ErrorIs(t, s.repo.AddItem(ctx, g, owner, rid, gdIron, "v1", 5, maxItems), ErrItemExists)
	require.ErrorIs(t, s.repo.AddItem(ctx, g, owner, rid, gdCopr, "v1", 5, 1), ErrMaxItems)

	prog, err := s.repo.ProgressByIDOwned(ctx, g, owner, rid)
	require.NoError(t, err)
	require.Len(t, prog.Items, 1)
	itemID := prog.Items[0].ID

	// A member reserves 4; the required qty can't drop below that.
	require.NoError(t, s.repo.Reserve(ctx, g, "th-items", gdIron, member, 4))
	require.ErrorIs(t, s.repo.UpdateItemQty(ctx, g, owner, itemID, 3), ErrQtyBelowReserved)
	require.NoError(t, s.repo.UpdateItemQty(ctx, g, owner, itemID, 8))

	// Removing the item clears its reservations and reports the count.
	cleared, err := s.repo.RemoveItem(ctx, g, owner, itemID)
	require.NoError(t, err)
	assert.Equal(t, 1, cleared)
	prog, err = s.repo.ProgressByIDOwned(ctx, g, owner, rid)
	require.NoError(t, err)
	assert.Empty(t, prog.Items)
}

func (s *supplySuite) TestDestinationAndMessageRef() {
	t := s.T()
	ctx, g := s.seed()
	rid := s.newRequest(ctx, g, owner, "th-dest")

	require.NoError(t, s.repo.SetDeliveryLocation(ctx, g, owner, rid, "Station_Cairn", "v1"))
	planet := 15
	require.NoError(t, s.repo.SetSystemInfo(ctx, g, owner, rid, "Muvalis", "QR-439F", &planet))
	require.NoError(t, s.repo.SetMessageRef(ctx, g, owner, rid, "111", "222", "333"))

	prog, err := s.repo.ProgressByIDOwned(ctx, g, owner, rid)
	require.NoError(t, err)
	assert.Equal(t, "Station_Cairn", prog.LocationGDID)
	assert.Equal(t, "Muvalis", prog.SystemName)
	assert.Equal(t, "QR-439F", prog.SystemCode)
	require.NotNil(t, prog.PlanetNumber)
	assert.Equal(t, 15, *prog.PlanetNumber)
	require.NotNil(t, prog.RefMessage)
	assert.Equal(t, "https://discord.com/channels/111/222/333", prog.RefMessage.Link())

	// Clearing nulls each independently (the pair CHECK holds).
	require.NoError(t, s.repo.SetDeliveryLocation(ctx, g, owner, rid, "", ""))
	require.NoError(t, s.repo.SetSystemInfo(ctx, g, owner, rid, "", "", nil))
	require.NoError(t, s.repo.SetMessageRef(ctx, g, owner, rid, "", "", ""))
	prog, err = s.repo.ProgressByIDOwned(ctx, g, owner, rid)
	require.NoError(t, err)
	assert.Empty(t, prog.LocationGDID)
	assert.Empty(t, prog.SystemName)
	assert.Nil(t, prog.PlanetNumber)
	assert.Nil(t, prog.RefMessage)

	// A non-positive planet violates the CHECK and surfaces as an error.
	bad := 0
	require.Error(t, s.repo.SetSystemInfo(ctx, g, owner, rid, "", "", &bad))
}

func (s *supplySuite) TestPanelSemantics() {
	t := s.T()
	ctx, g := s.seed()
	rid := s.newRequest(ctx, g, owner, "th-panel")
	require.NoError(t, s.repo.AddItem(ctx, g, owner, rid, gdIron, "v1", 10, maxItems))

	// Reserve is capped at what remains.
	require.ErrorIs(t, s.repo.Reserve(ctx, g, "th-panel", gdIron, member, 11), ErrOverCap)
	require.NoError(t, s.repo.Reserve(ctx, g, "th-panel", gdIron, member, 6))
	require.ErrorIs(t, s.repo.Reserve(ctx, g, "th-panel", gdIron, member, 5), ErrOverCap) // 6+5 > 10

	// Deliver is bounded by the member's own reservation.
	_, err := s.repo.Deliver(ctx, g, "th-panel", gdIron, member, 7)
	require.ErrorIs(t, err, ErrOverReserved)
	complete, err := s.repo.Deliver(ctx, g, "th-panel", gdIron, member, 4)
	require.NoError(t, err)
	assert.False(t, complete, "still short of required")

	// Release is floored at what's delivered.
	require.ErrorIs(t, s.repo.Release(ctx, g, "th-panel", gdIron, member, 3), ErrBelowDelivered) // reserved 6, delivered 4 → max release 2
	require.NoError(t, s.repo.Release(ctx, g, "th-panel", gdIron, member, 2))

	// Reserve the rest and deliver to full → same-tx completion + close task.
	require.NoError(t, s.repo.Reserve(ctx, g, "th-panel", gdIron, member, 6)) // reserved back to 4+6=... reserved now 4 (after release) +6 = 10
	complete, err = s.repo.Deliver(ctx, g, "th-panel", gdIron, member, 6)     // delivered 4+6=10 == required
	require.NoError(t, err)
	assert.True(t, complete)
	assert.Equal(t, "completed", s.status(ctx, rid))
	assert.GreaterOrEqual(t, s.taskCount(ctx, taskClose, rid), 1, "completion enqueues a close task")

	// Ops on a closed request are rejected; an unknown thread is not found.
	require.ErrorIs(t, s.repo.Reserve(ctx, g, "th-panel", gdIron, member, 1), ErrClosed)
	require.ErrorIs(t, s.repo.Reserve(ctx, g, "nope", gdIron, member, 1), ErrNotFound)
}

func (s *supplySuite) TestListByOwner() {
	t := s.T()
	ctx, g := s.seed()
	// Two open, one cancelled for owner; one open for other.
	a := s.newRequest(ctx, g, owner, "l-a")
	_ = s.newRequest(ctx, g, owner, "l-b")
	c := s.newRequest(ctx, g, owner, "l-c")
	require.NoError(t, s.repo.Cancel(ctx, g, owner, c))
	_ = s.newRequest(ctx, g, other, "l-d")

	open, total, err := s.repo.ListByOwner(ctx, g, owner, []Status{StatusOpen}, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, open, 2)

	all, total, err := s.repo.ListByOwner(ctx, g, owner, []Status{StatusOpen, StatusCancelled}, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, all, 3)

	// Pagination.
	page1, total, err := s.repo.ListByOwner(ctx, g, owner, []Status{StatusOpen, StatusCancelled}, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, page1, 2)
	page2, _, err := s.repo.ListByOwner(ctx, g, owner, []Status{StatusOpen, StatusCancelled}, 2, 2)
	require.NoError(t, err)
	assert.Len(t, page2, 1)

	_ = a
}

func (s *supplySuite) TestRepublish() {
	t := s.T()
	ctx, g := s.seed()

	// No thread yet → Republish enqueues a create.
	rid, err := s.repo.Create(ctx, CreateInput{ServerID: g, OwnerUserID: owner, Title: "t", OpenLimit: 10})
	require.NoError(t, err)
	before := s.taskCount(ctx, taskCreate, rid)
	require.NoError(t, s.repo.Republish(ctx, g, owner, rid))
	assert.Equal(t, before+1, s.taskCount(ctx, taskCreate, rid))

	// With a live thread → Republish enqueues a refresh.
	require.NoError(t, s.repo.SetThreadID(ctx, rid, "th-repub"))
	require.NoError(t, s.repo.Republish(ctx, g, owner, rid))
	assert.GreaterOrEqual(t, s.taskCount(ctx, taskRefresh, rid), 1)
}

func (s *supplySuite) TestConcurrentCreateAtLimit() {
	t := s.T()
	ctx, g := s.seed()
	// Seed one open request; limit 2 leaves exactly one slot.
	_ = s.newRequest(ctx, g, owner, "seed")

	var wg sync.WaitGroup
	results := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, results[idx] = s.repo.Create(ctx, CreateInput{ServerID: g, OwnerUserID: owner, Title: "race", OpenLimit: 2})
		}(i)
	}
	wg.Wait()

	success, limited := 0, 0
	for _, err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrLimit) {
			limited++
		} else {
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, success, "the advisory-locked count guard admits exactly one")
	assert.Equal(t, 3, limited)
}
