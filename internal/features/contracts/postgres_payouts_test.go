package contracts

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

// complete drives a one-item contract to completed via the public deliver path.
func (s *contractsSuite) complete(ctx context.Context, g uuid.UUID, threadID string) uuid.UUID {
	t := s.T()
	s.newContract(ctx, g, threadID)
	require.NoError(t, s.addItem(ctx, g, threadID, "Steel", 10, 25, mgr))
	require.NoError(t, s.repo.Participate(ctx, g, threadID, "Steel", u1, 10))
	done, err := s.repo.Deliver(ctx, g, threadID, "Steel", u1, 10)
	require.NoError(t, err)
	require.True(t, done)
	return s.cidOf(ctx, g, threadID)
}

// payoutTasks counts the payout outbox rows enqueued for a contract.
func (s *contractsSuite) payoutTasks(ctx context.Context, cid uuid.UUID) int {
	var n int
	require.NoError(s.T(), s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_tasks WHERE kind = $1 AND chronometric_id = $2`,
		taskPayout, cid).Scan(&n))
	return n
}

// TestCompletion_EnqueuesPayoutOnce enqueues exactly one payout task in the
// completing transaction — and only when both the credit reward and the factor
// are positive.
func (s *contractsSuite) TestCompletion_EnqueuesPayoutOnce() {
	t := s.T()
	repo, ctx, g := s.seed()

	// Rewarded contract: one payout task rides the completion.
	s.newContract(ctx, g, thread)
	cid := s.cidOf(ctx, g, thread)
	require.NoError(t, repo.UpdateRewards(ctx, g, cid, decPtr("100"), dec("50"), nil, nil, mgr))
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 10, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 10))
	done, err := repo.Deliver(ctx, g, thread, "Steel", u1, 10)
	require.NoError(t, err)
	require.True(t, done)
	assert.Equal(t, 1, s.payoutTasks(ctx, cid))

	// Zero factor: no payout task.
	s.newContract(ctx, g, "thread-nofactor")
	cid2 := s.cidOf(ctx, g, "thread-nofactor")
	require.NoError(t, repo.UpdateRewards(ctx, g, cid2, decPtr("100"), decimal.Zero, nil, nil, mgr))
	require.NoError(t, s.addItem(ctx, g, "thread-nofactor", "Steel", 5, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, "thread-nofactor", "Steel", u1, 5))
	done, err = repo.Deliver(ctx, g, "thread-nofactor", "Steel", u1, 5)
	require.NoError(t, err)
	require.True(t, done)
	assert.Zero(t, s.payoutTasks(ctx, cid2))

	// Factor set but no credit reward: no payout task.
	s.newContract(ctx, g, "thread-nocredits")
	cid3 := s.cidOf(ctx, g, "thread-nocredits")
	require.NoError(t, repo.UpdateRewards(ctx, g, cid3, nil, dec("50"), nil, nil, mgr))
	require.NoError(t, s.addItem(ctx, g, "thread-nocredits", "Steel", 5, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, "thread-nocredits", "Steel", u1, 5))
	done, err = repo.Deliver(ctx, g, "thread-nocredits", "Steel", u1, 5)
	require.NoError(t, err)
	require.True(t, done)
	assert.Zero(t, s.payoutTasks(ctx, cid3))
}

// TestCancelAndExpire_NoPayout: only completion pays out — the other terminal
// transitions never enqueue the task even with rewards + factor set.
func (s *contractsSuite) TestCancelAndExpire_NoPayout() {
	t := s.T()
	repo, ctx, g := s.seed()

	s.newContract(ctx, g, thread)
	cid := s.cidOf(ctx, g, thread)
	require.NoError(t, repo.UpdateRewards(ctx, g, cid, decPtr("100"), dec("50"), nil, nil, mgr))
	require.NoError(t, repo.CancelByID(ctx, g, cid, mgr))
	assert.Zero(t, s.payoutTasks(ctx, cid))

	// Rewards ride the create: the past deadline trips the lazy expiry guard on
	// any later mutation.
	cid2, err := repo.Create(ctx, CreateInput{
		ServerID: g, Kind: KindCustom, Title: "Doomed", Deadline: ptrTime(time.Now().Add(-time.Minute)),
		RewardCredits: decPtr("100"), ParticipantRewardFactor: dec("50"), CreatedByUserID: mgr,
	})
	require.NoError(t, err)
	moved, err := repo.MarkExpired(ctx, cid2, time.Now())
	require.NoError(t, err)
	require.True(t, moved)
	assert.Zero(t, s.payoutTasks(ctx, cid2))
}

func (s *contractsSuite) TestPayouts_SaveReadIdempotent() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.complete(ctx, g, thread)

	// Nothing yet: the worker keys "already computed" on this.
	rows, err := repo.Payouts(ctx, cid)
	require.NoError(t, err)
	assert.Empty(t, rows)

	require.NoError(t, repo.SavePayouts(ctx, cid, []Payout{
		{UserID: "zed", UserName: "Zed", Amount: dec("10.50"), SharePercent: dec("21")},
		{UserID: "amy", UserName: "Amy", Amount: dec("39.50"), SharePercent: dec("79")},
	}, 2))

	rows, err = repo.Payouts(ctx, cid)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "amy", rows[0].UserID, "ordered by user id")
	assert.Equal(t, "Amy", rows[0].UserName)
	assert.True(t, rows[0].Amount.Equal(dec("39.50")), "got %s", rows[0].Amount)
	assert.True(t, rows[1].SharePercent.Equal(dec("21")), "got %s", rows[1].SharePercent)

	// SavePayouts froze the compute precision on the contract row.
	prog, err := repo.ProgressByID(ctx, cid)
	require.NoError(t, err)
	require.NotNil(t, prog.PayoutDecimals)
	assert.Equal(t, int32(2), *prog.PayoutDecimals)

	// A retry (possibly with drifted catalog prices, or a changed config) never
	// alters posted figures — nor the frozen precision (first-write-wins).
	require.NoError(t, repo.SavePayouts(ctx, cid, []Payout{
		{UserID: "amy", UserName: "Amy!", Amount: dec("1"), SharePercent: dec("1")},
	}, 0))
	rows, err = repo.Payouts(ctx, cid)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.True(t, rows[0].Amount.Equal(dec("39.50")), "conflict rows stay untouched, got %s", rows[0].Amount)
	prog, err = repo.ProgressByID(ctx, cid)
	require.NoError(t, err)
	require.NotNil(t, prog.PayoutDecimals)
	assert.Equal(t, int32(2), *prog.PayoutDecimals, "frozen precision is first-write-wins, not restamped")
}

func (s *contractsSuite) TestRequestPayoutRepost() {
	t := s.T()
	repo, ctx, g := s.seed()

	// Open contract: no reprint (forged/stale button).
	open := s.newContract(ctx, g, "thread-open")
	require.ErrorIs(t, repo.RequestPayoutRepost(ctx, g, open), ErrNotFound)

	// Completed but rewardless: nothing was ever paid out — nothing to reprint.
	bare := s.complete(ctx, g, "thread-bare")
	require.ErrorIs(t, repo.RequestPayoutRepost(ctx, g, bare), ErrNotFound)

	// Completed with credits + factor: the real reprint path.
	s.newContract(ctx, g, thread)
	cid := s.cidOf(ctx, g, thread)
	require.NoError(t, repo.UpdateRewards(ctx, g, cid, decPtr("100"), dec("50"), nil, nil, mgr))
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 10, 25, mgr))
	require.NoError(t, s.repo.Participate(ctx, g, thread, "Steel", u1, 10))
	done, err := s.repo.Deliver(ctx, g, thread, "Steel", u1, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.NoError(t, repo.RequestPayoutRepost(ctx, g, cid))

	// Cross-server: scoped out.
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	require.ErrorIs(t, repo.RequestPayoutRepost(ctx, g2, cid), ErrNotFound)

	// The enqueued task carries the repost flag.
	var payload []byte
	require.NoError(t, s.Pool.QueryRow(ctx,
		`SELECT payload FROM outbox_tasks WHERE kind = $1 AND chronometric_id = $2 ORDER BY created_at DESC LIMIT 1`,
		taskPayout, cid).Scan(&payload))
	var tp taskPayload
	require.NoError(t, json.Unmarshal(payload, &tp))
	assert.True(t, tp.Repost)
	assert.Equal(t, cid, tp.ContractID)
}

func (s *contractsSuite) TestMarkPayoutPosted() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.complete(ctx, g, thread)

	p, err := repo.ProgressByID(ctx, cid)
	require.NoError(t, err)
	require.Nil(t, p.PayoutPostedAt)

	require.NoError(t, repo.MarkPayoutPosted(ctx, cid, "chan-1", "msg-1", time.Now()))
	p, err = repo.ProgressByID(ctx, cid)
	require.NoError(t, err)
	assert.NotNil(t, p.PayoutPostedAt)
	assert.Equal(t, "chan-1", p.PayoutReportChannelID, "the report location is recorded for edit-in-place")
	assert.Equal(t, "msg-1", p.PayoutReportMessageID)
}

func (s *contractsSuite) TestMarkPayoutsPaid_GuardedOnce() {
	t := s.T()
	repo, ctx, g := s.seed()

	// An open contract cannot be marked paid.
	open := s.newContract(ctx, g, "thread-open")
	ok, err := repo.MarkPayoutsPaid(ctx, g, open, mgr, time.Now())
	require.NoError(t, err)
	assert.False(t, ok)

	cid := s.complete(ctx, g, thread)

	// Wrong server: guarded, no write.
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	ok, err = repo.MarkPayoutsPaid(ctx, g2, cid, mgr, time.Now())
	require.NoError(t, err)
	assert.False(t, ok)

	// First press wins and records who paid.
	ok, err = repo.MarkPayoutsPaid(ctx, g, cid, mgr, time.Now())
	require.NoError(t, err)
	assert.True(t, ok)
	p, err := repo.ProgressByID(ctx, cid)
	require.NoError(t, err)
	require.NotNil(t, p.PayoutsPaidAt)
	assert.Equal(t, mgr, p.PayoutsPaidByUserID)

	// Second press loses.
	ok, err = repo.MarkPayoutsPaid(ctx, g, cid, "officer-2", time.Now())
	require.NoError(t, err)
	assert.False(t, ok)
	p, err = repo.ProgressByID(ctx, cid)
	require.NoError(t, err)
	assert.Equal(t, mgr, p.PayoutsPaidByUserID, "the first marker sticks")
}
