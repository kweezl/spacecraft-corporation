package contracts

import (
	"context"
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
// a thread, simulating the worker that normally creates the forum thread (create
// is async; the row starts with a NULL thread_id).
func (s *contractsSuite) newContract(ctx context.Context, g uuid.UUID, threadID string) uuid.UUID {
	id, err := s.repo.Create(ctx, CreateInput{
		ServerID: g, Title: "Steel Run",
		Deadline: time.Now().Add(24 * time.Hour), CreatedByUserID: mgr,
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.repo.SetThreadID(ctx, id, threadID))
	return id
}

func (s *contractsSuite) TestCreateAndProgress() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)

	require.NoError(t, repo.AddItem(ctx, g, thread, "Steel", 500, 25, mgr))
	require.NoError(t, repo.AddItem(ctx, g, thread, "Iron Plate", 1000, 25, mgr))

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusOpen, p.Status)
	require.Len(t, p.Items, 2)
	assert.Equal(t, "Steel", p.Items[0].Name)
	assert.Equal(t, 500, p.Items[0].RequiredQty)
	assert.Equal(t, 0, p.Items[0].ReservedQty)
}

func (s *contractsSuite) TestProgress_Participants() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, repo.AddItem(ctx, g, thread, "Steel", 1000, 25, mgr))

	// Reserve out of user order to prove Progress sorts contributors by user id.
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 100))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 200))
	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 50)
	require.NoError(t, err)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	require.Len(t, p.Items, 1)
	parts := p.Items[0].Participants
	require.Len(t, parts, 2)
	// Ordered by user id: u1 ("user-1") before u2 ("user-2").
	assert.Equal(t, Participant{UserID: u1, Reserved: 200, Delivered: 50}, parts[0])
	assert.Equal(t, Participant{UserID: u2, Reserved: 100, Delivered: 0}, parts[1])
}

func (s *contractsSuite) TestAddItem_DuplicateAndLimit() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)

	require.NoError(t, repo.AddItem(ctx, g, thread, "Steel", 500, 2, mgr))
	// Case-insensitive duplicate is refused.
	require.ErrorIs(t, repo.AddItem(ctx, g, thread, "steel", 10, 2, mgr), ErrItemExists)
	require.NoError(t, repo.AddItem(ctx, g, thread, "Iron", 10, 2, mgr))
	// Third exceeds MaxItems=2.
	require.ErrorIs(t, repo.AddItem(ctx, g, thread, "Copper", 10, 2, mgr), ErrMaxItems)
}

func (s *contractsSuite) TestParticipate_CapAndAdditive() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, repo.AddItem(ctx, g, thread, "Steel", 1000, 25, mgr))

	// Additive for the same member: 100 then 10 => 110 reserved.
	require.NoError(t, repo.Participate(ctx, g, thread, "steel", u1, 100))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 10))
	// Another member fills toward the cap.
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 800))
	// Remaining is 1000-110-800 = 90; 91 is over the cap.
	require.ErrorIs(t, repo.Participate(ctx, g, thread, "Steel", u2, 91), ErrOverCap)
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 90))

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, 1000, p.Items[0].ReservedQty)
	assert.Equal(t, 0, p.Items[0].Remaining())
}

func (s *contractsSuite) TestParticipate_UnknownItem() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.ErrorIs(t, repo.Participate(ctx, g, thread, "Ghost", u1, 1), ErrItemNotFound)
}

func (s *contractsSuite) TestDeliver_BoundsAndCompletion() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, repo.AddItem(ctx, g, thread, "Steel", 100, 25, mgr))

	// Must reserve before delivering.
	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 10)
	require.ErrorIs(t, err, ErrNoReservation)

	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 60))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 40))

	// Can't deliver more than reserved.
	_, err = repo.Deliver(ctx, g, thread, "Steel", u1, 61)
	require.ErrorIs(t, err, ErrOverReserved)

	complete, err := repo.Deliver(ctx, g, thread, "Steel", u1, 60)
	require.NoError(t, err)
	assert.False(t, complete, "still missing u2's 40")

	complete, err = repo.Deliver(ctx, g, thread, "Steel", u2, 40)
	require.NoError(t, err)
	assert.True(t, complete, "all items fully delivered")

	// Contract is now completed; further mutations are refused.
	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, p.Status)
	require.ErrorIs(t, repo.Participate(ctx, g, thread, "Steel", u1, 1), ErrClosed)
}

func (s *contractsSuite) TestRelease_FloorAtDelivered() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, repo.AddItem(ctx, g, thread, "Steel", 1000, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 100))
	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 10)
	require.NoError(t, err)

	// Max releasable is 100-10 = 90; 91 is refused.
	require.ErrorIs(t, repo.Release(ctx, g, thread, "Steel", u1, 91, u1), ErrBelowDelivered)
	require.NoError(t, repo.Release(ctx, g, thread, "Steel", u1, 90, u1))

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, 10, p.Items[0].ReservedQty, "reservation floored at delivered")
	assert.Equal(t, 10, p.Items[0].DeliveredQty)
}

func (s *contractsSuite) TestReleaseMember_ByOfficer_AndFreesCap() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, repo.AddItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 100))
	// Cap is full; u2 cannot reserve.
	require.ErrorIs(t, repo.Participate(ctx, g, thread, "Steel", u2, 1), ErrOverCap)

	// Officer releases u1's full (undelivered) reservation on their behalf.
	require.NoError(t, repo.Release(ctx, g, thread, "Steel", u1, 100, mgr))

	// The reservation row is gone and capacity is freed.
	out, err := repo.MemberOutstanding(ctx, g, thread, u1)
	require.NoError(t, err)
	assert.Empty(t, out)
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 100))
}

func (s *contractsSuite) TestRemoveItem_CascadesReservations() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, repo.AddItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 40))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 30))

	cleared, err := repo.RemoveItem(ctx, g, thread, "steel", mgr)
	require.NoError(t, err)
	assert.Equal(t, 2, cleared)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Empty(t, p.Items)
}

func (s *contractsSuite) TestExpiry_LazyGuardAndSweep() {
	t := s.T()
	repo, ctx, g := s.seed()
	// Create with an already-past deadline, then assign its thread (as the worker would).
	id, err := repo.Create(ctx, CreateInput{
		ServerID: g, Title: "Late",
		Deadline: time.Now().Add(-time.Minute), CreatedByUserID: mgr,
	})
	require.NoError(t, err)
	require.NoError(t, repo.SetThreadID(ctx, id, thread))

	// Mutations refuse a past-deadline (still 'open') contract.
	require.ErrorIs(t, repo.AddItem(ctx, g, thread, "Steel", 1, 25, mgr), ErrExpired)

	// The sweeper sees it as due and flips it once (idempotent).
	due, err := repo.DueContracts(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, id, due[0])

	flipped, err := repo.MarkExpired(ctx, id, time.Now())
	require.NoError(t, err)
	assert.True(t, flipped)
	flipped, err = repo.MarkExpired(ctx, id, time.Now())
	require.NoError(t, err)
	assert.False(t, flipped, "already expired: no second transition")

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusExpired, p.Status)
}

func (s *contractsSuite) TestCancel() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, repo.Cancel(ctx, g, thread, mgr))
	require.ErrorIs(t, repo.Cancel(ctx, g, thread, mgr), ErrClosed)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, p.Status)
}

func (s *contractsSuite) TestList_FilterAndPagination() {
	t := s.T()
	repo, ctx, g := s.seed()

	// Three open contracts on distinct threads + one cancelled.
	for _, th := range []string{"t-a", "t-b", "t-c"} {
		s.newContract(ctx, g, th)
	}
	s.newContract(ctx, g, "t-x")
	require.NoError(t, repo.Cancel(ctx, g, "t-x", mgr))
	require.NoError(t, repo.AddItem(ctx, g, "t-a", "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, "t-a", "Steel", u1, 40))
	_, err := repo.Deliver(ctx, g, "t-a", "Steel", u1, 25)
	require.NoError(t, err)

	// Default (open) excludes the cancelled one.
	page, total, err := repo.List(ctx, g, "", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, page, 3)

	// "all" includes the cancelled one.
	_, total, err = repo.List(ctx, g, "all", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, total)

	// Roll-up totals for t-a.
	_, total, err = repo.List(ctx, g, "cancelled", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total)

	// Pagination: 2 per page over 3 open.
	p1, total, err := repo.List(ctx, g, "open", 2, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	require.Len(t, p1, 2)
	p2, _, err := repo.List(ctx, g, "open", 2, 2)
	require.NoError(t, err)
	require.Len(t, p2, 1)
}

func (s *contractsSuite) TestServerIsolation() {
	t := s.T()
	repo, ctx, g1 := s.seed()
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	s.newContract(ctx, g1, thread)
	require.NoError(t, repo.AddItem(ctx, g1, thread, "Steel", 100, 25, mgr))

	// A different server can't see or mutate the contract on the same thread id.
	_, err := repo.Progress(ctx, g2, thread)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, repo.Participate(ctx, g2, thread, "Steel", u1, 1), ErrNotFound)
}
