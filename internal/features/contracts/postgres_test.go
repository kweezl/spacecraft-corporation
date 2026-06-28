package contracts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/kweezl/spacecraft-corporation/internal/outbox"
	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

const (
	u1     = "user-1"
	u2     = "user-2"
	mgr    = "officer-1"
	thread = "thread-1"
)

func ptrTime(t time.Time) *time.Time { return &t }

type contractsSuite struct {
	testdb.Suite
	repo Repository
}

func (s *contractsSuite) SetupSuite() {
	s.Suite.SetupSuite()
	s.repo = newRepository(s.Pool, outbox.NewEnqueuer())
}

func TestPgRepository(t *testing.T) { suite.Run(t, new(contractsSuite)) }

func (s *contractsSuite) seed() (Repository, context.Context, uuid.UUID) {
	g1 := testdb.SeedServer(s.T(), s.Pool, "g1")
	return s.repo, context.Background(), g1
}

// newContract creates an open contract with a far-future deadline and assigns it
// a thread, simulating the worker that normally creates the forum thread.
func (s *contractsSuite) newContract(ctx context.Context, g uuid.UUID, threadID string) uuid.UUID {
	return s.newContractDeadline(ctx, g, threadID, time.Now().Add(24*time.Hour))
}

// --- thread→id adapters: the console keys mutations by UUID, so these resolve a
// thread to its contract/item ids to keep the test bodies readable. ---

func (s *contractsSuite) cidOf(ctx context.Context, g uuid.UUID, threadID string) uuid.UUID {
	p, err := s.repo.Progress(ctx, g, threadID)
	require.NoError(s.T(), err)
	return p.ID
}

func (s *contractsSuite) itemID(ctx context.Context, g uuid.UUID, threadID, name string) uuid.UUID {
	p, err := s.repo.Progress(ctx, g, threadID)
	require.NoError(s.T(), err)
	for _, it := range p.Items {
		if strings.EqualFold(it.Name, name) {
			return it.ID
		}
	}
	s.T().Fatalf("item %q not found on %q", name, threadID)
	return uuid.Nil
}

func (s *contractsSuite) addItem(ctx context.Context, g uuid.UUID, threadID, name string, qty, maxItems int, actor string) error {
	return s.repo.AddItemByID(ctx, g, s.cidOf(ctx, g, threadID), name, qty, maxItems, actor)
}

func (s *contractsSuite) cancel(ctx context.Context, g uuid.UUID, threadID, actor string) error {
	return s.repo.CancelByID(ctx, g, s.cidOf(ctx, g, threadID), actor)
}

func (s *contractsSuite) TestCreateAndProgress() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)

	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 500, 25, mgr))
	require.NoError(t, s.addItem(ctx, g, thread, "Iron Plate", 1000, 25, mgr))

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusOpen, p.Status)
	require.Len(t, p.Items, 2)
	assert.Equal(t, "Steel", p.Items[0].Name)
	assert.Equal(t, 500, p.Items[0].RequiredQty)
}

func (s *contractsSuite) TestProgress_Participants() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 1000, 25, mgr))

	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 100))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 200))
	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 50)
	require.NoError(t, err)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	require.Len(t, p.Items, 1)
	parts := p.Items[0].Participants
	require.Len(t, parts, 2)
	assert.Equal(t, Participant{UserID: u1, Reserved: 200, Delivered: 50}, parts[0])
	assert.Equal(t, Participant{UserID: u2, Reserved: 100, Delivered: 0}, parts[1])
}

func (s *contractsSuite) TestAddItem_DuplicateAndLimit() {
	t := s.T()
	_, ctx, g := s.seed()
	s.newContract(ctx, g, thread)

	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 500, 2, mgr))
	require.ErrorIs(t, s.addItem(ctx, g, thread, "steel", 10, 2, mgr), ErrItemExists)
	require.NoError(t, s.addItem(ctx, g, thread, "Iron", 10, 2, mgr))
	require.ErrorIs(t, s.addItem(ctx, g, thread, "Copper", 10, 2, mgr), ErrMaxItems)
}

func (s *contractsSuite) TestParticipate_CapAndAdditive() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 1000, 25, mgr))

	require.NoError(t, repo.Participate(ctx, g, thread, "steel", u1, 100))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 10))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 800))
	require.ErrorIs(t, repo.Participate(ctx, g, thread, "Steel", u2, 91), ErrOverCap)
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 90))

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, 1000, p.Items[0].ReservedQty)
	assert.Equal(t, 0, p.Items[0].Remaining())
}

func (s *contractsSuite) TestDeliver_BoundsAndCompletion() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))

	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 10)
	require.ErrorIs(t, err, ErrNoReservation)

	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 60))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 40))

	_, err = repo.Deliver(ctx, g, thread, "Steel", u1, 61)
	require.ErrorIs(t, err, ErrOverReserved)

	complete, err := repo.Deliver(ctx, g, thread, "Steel", u1, 60)
	require.NoError(t, err)
	assert.False(t, complete)

	complete, err = repo.Deliver(ctx, g, thread, "Steel", u2, 40)
	require.NoError(t, err)
	assert.True(t, complete)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, p.Status)
	require.ErrorIs(t, repo.Participate(ctx, g, thread, "Steel", u1, 1), ErrClosed)
}

func (s *contractsSuite) TestRelease_FloorAtDelivered() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 1000, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 100))
	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 10)
	require.NoError(t, err)

	require.ErrorIs(t, repo.Release(ctx, g, thread, "Steel", u1, 91, u1), ErrBelowDelivered)
	require.NoError(t, repo.Release(ctx, g, thread, "Steel", u1, 90, u1))

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, 10, p.Items[0].ReservedQty)
	assert.Equal(t, 10, p.Items[0].DeliveredQty)
}

// --- console (id-keyed) repository methods ---

func (s *contractsSuite) TestReleaseByItem_ByOfficer() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 100))
	require.ErrorIs(t, repo.Participate(ctx, g, thread, "Steel", u2, 1), ErrOverCap)

	itemID := s.itemID(ctx, g, thread, "Steel")
	cid, err := repo.ReleaseByItem(ctx, g, itemID, u1, 100, mgr)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, cid)

	out, err := repo.MemberOutstanding(ctx, g, thread, u1)
	require.NoError(t, err)
	assert.Empty(t, out)
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 100))
}

func (s *contractsSuite) TestRemoveReservation() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 40))
	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 10)
	require.NoError(t, err)

	itemID := s.itemID(ctx, g, thread, "Steel")
	_, err = repo.RemoveReservation(ctx, g, itemID, u1, mgr)
	require.NoError(t, err)
	// Removing a non-existent reservation is a no-op error.
	_, err = repo.RemoveReservation(ctx, g, itemID, u1, mgr)
	require.ErrorIs(t, err, ErrNoReservation)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Empty(t, p.Items[0].Participants)
}

func (s *contractsSuite) TestRemoveItemByID_CascadesReservations() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 40))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 30))

	itemID := s.itemID(ctx, g, thread, "Steel")
	cid, cleared, err := repo.RemoveItemByID(ctx, g, itemID, mgr)
	require.NoError(t, err)
	assert.Equal(t, 2, cleared)
	assert.Equal(t, s.cidOf(ctx, g, thread), cid)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Empty(t, p.Items)
}

func (s *contractsSuite) TestUpdateItemName_Collision() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, s.addItem(ctx, g, thread, "Iron", 100, 25, mgr))

	steel := s.itemID(ctx, g, thread, "Steel")
	_, err := repo.UpdateItemName(ctx, g, steel, "iron", mgr)
	require.ErrorIs(t, err, ErrItemExists)
	_, err = repo.UpdateItemName(ctx, g, steel, "Titanium", mgr)
	require.NoError(t, err)
	assert.Equal(t, "Titanium", s.byName(ctx, g, thread, "Titanium").Name)
}

func (s *contractsSuite) byName(ctx context.Context, g uuid.UUID, threadID, name string) Item {
	p, err := s.repo.Progress(ctx, g, threadID)
	require.NoError(s.T(), err)
	for _, it := range p.Items {
		if it.Name == name {
			return it
		}
	}
	s.T().Fatalf("item %q not found", name)
	return Item{}
}

func (s *contractsSuite) TestUpdateDetails() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)
	require.NoError(t, repo.UpdateDetails(ctx, g, cid, "New Title", "New desc", mgr))
	p, err := repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	assert.Equal(t, "New Title", p.Title)
	assert.Equal(t, "New desc", p.Description)
}

func (s *contractsSuite) TestSetDeadline_ClearAndSet() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)

	// Clear it: no deadline, never auto-expires.
	require.NoError(t, repo.SetDeadline(ctx, g, cid, nil, mgr))
	p, err := repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	assert.Nil(t, p.Deadline)
	due, err := repo.DueContracts(ctx, time.Now().Add(100*time.Hour), 10)
	require.NoError(t, err)
	assert.NotContains(t, due, cid, "a deadline-less contract is never due")

	// Set it back.
	require.NoError(t, repo.SetDeadline(ctx, g, cid, ptrTime(time.Now().Add(3*time.Hour)), mgr))
	p, err = repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	require.NotNil(t, p.Deadline)
}

func (s *contractsSuite) TestProgressScoped_ForgedIDs() {
	t := s.T()
	repo, ctx, g := s.seed()
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	cid := s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))
	itemID := s.itemID(ctx, g, thread, "Steel")

	// Wrong server / random id -> ErrNotFound (the zero-rows guarantee).
	_, err := repo.ProgressByIDScoped(ctx, g2, cid)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repo.ProgressByIDScoped(ctx, g, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repo.ProgressByItemScoped(ctx, g2, itemID)
	require.ErrorIs(t, err, ErrNotFound)

	// Right server resolves the whole contract from an item id.
	p, err := repo.ProgressByItemScoped(ctx, g, itemID)
	require.NoError(t, err)
	assert.Equal(t, cid, p.ID)
}

func (s *contractsSuite) TestExpiry_LazyGuardAndSweep() {
	t := s.T()
	repo, ctx, g := s.seed()
	id := s.newContractDeadline(ctx, g, thread, time.Now().Add(-time.Minute))

	// A past-deadline (still 'open') contract refuses console mutations.
	require.ErrorIs(t, s.addItem(ctx, g, thread, "Steel", 1, 25, mgr), ErrExpired)

	due, err := repo.DueContracts(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Contains(t, due, id)

	flipped, err := repo.MarkExpired(ctx, id, time.Now())
	require.NoError(t, err)
	assert.True(t, flipped)
	flipped, err = repo.MarkExpired(ctx, id, time.Now())
	require.NoError(t, err)
	assert.False(t, flipped)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusExpired, p.Status)
}

func (s *contractsSuite) TestNoDeadline_NotExpired() {
	t := s.T()
	_, ctx, g := s.seed()
	// Create with no deadline; console mutations are allowed (never "expired").
	id, err := s.repo.Create(ctx, CreateInput{ServerID: g, Title: "Endless", CreatedByUserID: mgr})
	require.NoError(t, err)
	require.NoError(t, s.repo.SetThreadID(ctx, id, thread))
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 1, 25, mgr))

	due, err := s.repo.DueContracts(ctx, time.Now().Add(1000*time.Hour), 10)
	require.NoError(t, err)
	assert.NotContains(t, due, id)
}

func (s *contractsSuite) TestCancelByID() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.cancel(ctx, g, thread, mgr))
	require.ErrorIs(t, s.cancel(ctx, g, thread, mgr), ErrClosed)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, p.Status)
}

func (s *contractsSuite) TestRepublishAndClearThread() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)

	// Thread set + open -> refresh.
	act, err := repo.Republish(ctx, g, cid)
	require.NoError(t, err)
	assert.Equal(t, RepublishRefreshing, act)

	// Clear the thread (post deleted) -> republish recreates.
	require.NoError(t, repo.ClearThreadID(ctx, cid))
	p, err := repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	assert.Empty(t, p.ThreadID)
	act, err = repo.Republish(ctx, g, cid)
	require.NoError(t, err)
	assert.Equal(t, RepublishCreating, act)
}

func (s *contractsSuite) TestList_MultiStatusAndCounts() {
	t := s.T()
	repo, ctx, g := s.seed()

	for _, th := range []string{"t-a", "t-b", "t-c"} {
		s.newContract(ctx, g, th)
	}
	s.newContract(ctx, g, "t-x")
	require.NoError(t, s.cancel(ctx, g, "t-x", mgr))
	// A deadline-less contract sorts last but is still listed.
	endless, err := repo.Create(ctx, CreateInput{ServerID: g, Title: "Endless", CreatedByUserID: mgr})
	require.NoError(t, err)
	require.NoError(t, repo.SetThreadID(ctx, endless, "t-endless"))

	require.NoError(t, s.addItem(ctx, g, "t-a", "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, "t-a", "Steel", u1, 40))
	_, err = repo.Deliver(ctx, g, "t-a", "Steel", u1, 25)
	require.NoError(t, err)

	// Open filter: 3 originals + the deadline-less one = 4.
	page, total, err := repo.List(ctx, g, []Status{StatusOpen}, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	require.Len(t, page, 4)

	// Multi-status open+cancelled = 5.
	_, total, err = repo.List(ctx, g, []Status{StatusOpen, StatusCancelled}, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 5, total)

	// Counts on t-a.
	var ta ListEntry
	for _, e := range page {
		if e.ThreadID == "t-a" {
			ta = e
		}
	}
	assert.Equal(t, 1, ta.ItemCount)
	assert.Equal(t, 1, ta.ParticipantCount)
	assert.Equal(t, 40, ta.TotalReserved)
	assert.Equal(t, 25, ta.TotalDelivered)
	assert.Equal(t, 100, ta.TotalRequired)
}

func (s *contractsSuite) TestServerIsolation() {
	t := s.T()
	repo, ctx, g1 := s.seed()
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	s.newContract(ctx, g1, thread)
	require.NoError(t, s.addItem(ctx, g1, thread, "Steel", 100, 25, mgr))

	_, err := repo.Progress(ctx, g2, thread)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, repo.Participate(ctx, g2, thread, "Steel", u1, 1), ErrNotFound)
}
