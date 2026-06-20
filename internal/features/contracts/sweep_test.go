package contracts

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newContractDeadline is newContract with a caller-chosen deadline, for the
// expiry/notice scans.
func (s *contractsSuite) newContractDeadline(ctx context.Context, g uuid.UUID, threadID string, deadline time.Time) uuid.UUID {
	id, err := s.repo.Create(ctx, CreateInput{
		ServerID: g, Title: "Steel Run", Deadline: deadline, CreatedByUserID: mgr,
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.repo.SetThreadID(ctx, id, threadID))
	return id
}

func contains(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// Create stamps last_refreshed_at, and every mutation advances it in lockstep
// with the refresh it enqueues.
func (s *contractsSuite) TestLastRefreshedAt_SetAndAdvances() {
	t := s.T()
	repo, ctx, g := s.seed()
	before := time.Now().Add(-time.Second)
	id := s.newContract(ctx, g, thread)

	p0, err := repo.ProgressByID(ctx, id)
	require.NoError(t, err)
	assert.False(t, p0.LastRefreshedAt.IsZero(), "create must stamp last_refreshed_at")
	assert.False(t, p0.LastRefreshedAt.Before(before))

	require.NoError(t, repo.AddItem(ctx, g, thread, "Steel", 500, 25, mgr))
	p1, err := repo.ProgressByID(ctx, id)
	require.NoError(t, err)
	assert.False(t, p1.LastRefreshedAt.Before(p0.LastRefreshedAt), "a mutation advances the watermark")
}

// StaleContracts selects open contracts last rendered at or before the cutoff;
// MarkRefreshed advances the watermark and only fires for open contracts.
func (s *contractsSuite) TestStaleContracts_AndMarkRefreshed() {
	t := s.T()
	repo, ctx, g := s.seed()
	id := s.newContract(ctx, g, thread)

	// A cutoff in the future includes the just-created contract; one in the past
	// excludes it.
	stale, err := repo.StaleContracts(ctx, time.Now().Add(time.Minute), 100)
	require.NoError(t, err)
	assert.True(t, contains(stale, id))
	stale, err = repo.StaleContracts(ctx, time.Now().Add(-time.Hour), 100)
	require.NoError(t, err)
	assert.False(t, contains(stale, id))

	p0, err := repo.ProgressByID(ctx, id)
	require.NoError(t, err)
	ok, err := repo.MarkRefreshed(ctx, id, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.True(t, ok)
	p1, err := repo.ProgressByID(ctx, id)
	require.NoError(t, err)
	assert.True(t, p1.LastRefreshedAt.After(p0.LastRefreshedAt), "MarkRefreshed advances the watermark")

	// A closed contract is never kept warm.
	require.NoError(t, repo.Cancel(ctx, g, thread, mgr))
	ok, err = repo.MarkRefreshed(ctx, id, time.Now())
	require.NoError(t, err)
	assert.False(t, ok)
}

// NotifyDue selects open, not-yet-notified contracts inside the window that have
// at least one participant; MarkNotified latches so the ping fires exactly once.
func (s *contractsSuite) TestNotifyDue_AndMarkNotified() {
	t := s.T()
	repo, ctx, g := s.seed()
	now := time.Now()

	soon := s.newContractDeadline(ctx, g, "t-soon", now.Add(30*time.Minute)) // inside 1h
	far := s.newContractDeadline(ctx, g, "t-far", now.Add(5*time.Hour))      // outside
	past := s.newContractDeadline(ctx, g, "t-past", now.Add(-time.Minute))   // already due
	// soon and far both have a participant, proving the window (not the participant
	// gate) is what excludes far. past is already past its deadline — mutations are
	// refused there, and it's excluded by deadline > now regardless.
	for _, th := range []string{"t-soon", "t-far"} {
		require.NoError(t, repo.AddItem(ctx, g, th, "Steel", 100, 25, mgr))
		require.NoError(t, repo.Participate(ctx, g, th, "Steel", u1, 10))
	}

	due, err := repo.NotifyDue(ctx, now, time.Hour, 100)
	require.NoError(t, err)
	assert.True(t, contains(due, soon), "within-window contract with a participant is notify-due")
	assert.False(t, contains(due, far), "far-future contract is not")
	assert.False(t, contains(due, past), "past-deadline contract expires, not notified")

	ok, err := repo.MarkNotified(ctx, soon, now)
	require.NoError(t, err)
	assert.True(t, ok)
	// Latched: a second mark is a no-op and it drops out of the scan.
	ok, err = repo.MarkNotified(ctx, soon, now)
	require.NoError(t, err)
	assert.False(t, ok)
	due, err = repo.NotifyDue(ctx, now, time.Hour, 100)
	require.NoError(t, err)
	assert.False(t, contains(due, soon))
}

// A contract that enters the notice window with zero participants is NOT latched,
// so the one-shot notice is preserved for when its first reservation arrives —
// the regression this guards (short-deadline contracts gaining participants after
// the window opens).
func (s *contractsSuite) TestNotifyDue_DefersUntilParticipant() {
	t := s.T()
	repo, ctx, g := s.seed()
	now := time.Now()
	id := s.newContractDeadline(ctx, g, thread, now.Add(20*time.Minute)) // inside the window

	// No reservations yet: not surfaced, and a direct latch attempt is a no-op.
	due, err := repo.NotifyDue(ctx, now, time.Hour, 100)
	require.NoError(t, err)
	assert.False(t, contains(due, id), "participant-less contract must not be notify-due")
	ok, err := repo.MarkNotified(ctx, id, now)
	require.NoError(t, err)
	assert.False(t, ok, "the latch is deferred — not consumed without a participant")

	// A member reserves -> now it becomes due, latch intact.
	require.NoError(t, repo.AddItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 10))
	due, err = repo.NotifyDue(ctx, now, time.Hour, 100)
	require.NoError(t, err)
	assert.True(t, contains(due, id), "once a participant exists the notice fires")
}

// OutstandingParticipantUserIDs returns each member who still owes delivery once
// across all items, and excludes members who have delivered everything reserved.
func (s *contractsSuite) TestOutstandingParticipantUserIDs() {
	t := s.T()
	repo, ctx, g := s.seed()
	id := s.newContract(ctx, g, thread)
	require.NoError(t, repo.AddItem(ctx, g, thread, "Steel", 1000, 25, mgr))
	require.NoError(t, repo.AddItem(ctx, g, thread, "Iron", 1000, 25, mgr))

	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 100))
	require.NoError(t, repo.Participate(ctx, g, thread, "Iron", u1, 100)) // u1 on two items
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 100))

	// u2 delivers everything they reserved -> no longer outstanding; u1 still owes.
	_, err := repo.Deliver(ctx, g, thread, "Steel", u2, 100)
	require.NoError(t, err)

	ids, err := repo.OutstandingParticipantUserIDs(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, []string{u1}, ids, "fully-delivered members are excluded; u1 listed once across items")
}
