package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

type outboxSuite struct {
	testdb.Suite
	enq Enqueuer
}

func (s *outboxSuite) SetupSuite() {
	s.Suite.SetupSuite()
	s.enq = NewEnqueuer()
}

func TestOutbox(t *testing.T) { suite.Run(t, new(outboxSuite)) }

func (s *outboxSuite) enqueue(req Request) {
	s.T().Helper()
	ctx := context.Background()
	tx, err := s.Pool.Begin(ctx)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.enq.Enqueue(ctx, tx, req))
	require.NoError(s.T(), tx.Commit(ctx))
}

func (s *outboxSuite) rowState(kind string) (status string, attempts int, lastErr string, evacuated bool) {
	s.T().Helper()
	var evac *time.Time
	require.NoError(s.T(), s.Pool.QueryRow(context.Background(),
		`SELECT status, attempts, last_error, evacuated_at FROM outbox_tasks WHERE kind = $1`, kind).
		Scan(&status, &attempts, &lastErr, &evac))
	return status, attempts, lastErr, evac != nil
}

func (s *outboxSuite) count() int {
	s.T().Helper()
	var n int
	require.NoError(s.T(), s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_tasks`).Scan(&n))
	return n
}

// recorder is a configurable handler capturing calls.
type recorder struct {
	mu      sync.Mutex
	calls   int
	payload []json.RawMessage
	err     error
}

func (r *recorder) handle(_ context.Context, t Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.payload = append(r.payload, t.Payload)
	return r.err
}

func (s *outboxSuite) worker(rec *recorder, maxAttempts int) *Worker {
	return newWorker(s.Pool, []Registration{{Kind: "test", Handler: rec.handle}},
		Config{BatchSize: 50, MaxAttempts: maxAttempts}, zap.NewNop())
}

func (s *outboxSuite) TestEnqueueAndSucceed() {
	t := s.T()
	s.enqueue(Request{Kind: "test", Payload: map[string]any{"n": 1}})
	rec := &recorder{}
	s.worker(rec, 3).processBatch(context.Background())

	assert.Equal(t, 1, rec.calls)
	status, _, _, _ := s.rowState("test")
	assert.Equal(t, "done", status)
}

func (s *outboxSuite) TestRetryThenFailEvacuates() {
	t := s.T()
	s.enqueue(Request{Kind: "test", Payload: map[string]any{}})
	rec := &recorder{err: errors.New("boom")}
	w := s.worker(rec, 2) // max attempts = 2

	w.processBatch(context.Background())
	status, attempts, lastErr, evac := s.rowState("test")
	assert.Equal(t, "pending", status, "first failure reschedules")
	assert.Equal(t, 1, attempts)
	assert.Contains(t, lastErr, "boom")
	assert.False(t, evac, "still pending: not evacuated")

	// Make it due again (retry pushed next_try_at into the future).
	_, err := s.Pool.Exec(context.Background(),
		`UPDATE outbox_tasks SET next_try_at = now() - interval '1 minute' WHERE kind = 'test'`)
	require.NoError(t, err)

	w.processBatch(context.Background())
	status, attempts, _, evac = s.rowState("test")
	assert.Equal(t, "failed", status, "second failure hits max attempts")
	assert.Equal(t, 2, attempts)
	assert.True(t, evac, "abandoned tasks are evacuated")
}

func (s *outboxSuite) TestPermanentFailsImmediately() {
	t := s.T()
	s.enqueue(Request{Kind: "test", Payload: map[string]any{}})
	rec := &recorder{err: Permanent(errors.New("no forum"))}
	s.worker(rec, 10).processBatch(context.Background())

	status, attempts, lastErr, evac := s.rowState("test")
	assert.Equal(t, "failed", status, "permanent errors are not retried")
	assert.Equal(t, 1, attempts)
	assert.Contains(t, lastErr, "no forum")
	assert.True(t, evac)
}

func (s *outboxSuite) TestHandlerPanicRecovered() {
	t := s.T()
	s.enqueue(Request{Kind: "test", Payload: map[string]any{}})
	// A handler that panics must not crash the worker goroutine (which would
	// silently stop every future outbox effect); it is treated as a failure.
	w := newWorker(s.Pool, []Registration{{Kind: "test", Handler: func(context.Context, Task) error {
		panic("kaboom")
	}}}, Config{BatchSize: 50, MaxAttempts: 3}, zap.NewNop())

	require.NotPanics(t, func() { w.processBatch(context.Background()) })
	status, attempts, lastErr, _ := s.rowState("test")
	assert.Equal(t, "pending", status, "a recovered panic reschedules like any other failure")
	assert.Equal(t, 1, attempts)
	assert.Contains(t, lastErr, "panic")
}

func (s *outboxSuite) TestChronometricSupersedesOlder() {
	t := s.T()
	chrono := uuid.New()
	s.enqueue(Request{Kind: "test", Payload: map[string]any{"v": 1}, ChronometricID: chrono})
	s.enqueue(Request{Kind: "test", Payload: map[string]any{"v": 2}, ChronometricID: chrono})
	assert.Equal(t, 2, s.count(), "each enqueue is its own durable row (no coalescing at enqueue)")

	rec := &recorder{}
	s.worker(rec, 3).processBatch(context.Background())
	require.Equal(t, 1, rec.calls, "only the newest task in the group runs")
	// It ran the newest payload (v=2); the older one was superseded.
	var got struct {
		V int `json:"v"`
	}
	require.NoError(t, json.Unmarshal(rec.payload[0], &got))
	assert.Equal(t, 2, got.V)

	var pending int
	require.NoError(t, s.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox_tasks WHERE status = 'pending'`).Scan(&pending))
	assert.Equal(t, 0, pending, "older task is superseded (done), newest is done")
}

func (s *outboxSuite) TestDifferentKindsNotGrouped() {
	t := s.T()
	chrono := uuid.New()
	// Same chronometric id but different kinds must NOT collapse (a refresh must
	// never supersede a create).
	rec := &recorder{}
	w := newWorker(s.Pool, []Registration{
		{Kind: "a", Handler: rec.handle},
		{Kind: "b", Handler: rec.handle},
	}, Config{BatchSize: 50, MaxAttempts: 3}, zap.NewNop())
	s.enqueue(Request{Kind: "a", ChronometricID: chrono})
	s.enqueue(Request{Kind: "b", ChronometricID: chrono})

	w.processBatch(context.Background())
	assert.Equal(t, 2, rec.calls, "different kinds run independently")
}

func (s *outboxSuite) TestNullChronometricEachRuns() {
	t := s.T()
	s.enqueue(Request{Kind: "test", Payload: map[string]any{}})
	s.enqueue(Request{Kind: "test", Payload: map[string]any{}})
	rec := &recorder{}
	s.worker(rec, 3).processBatch(context.Background())
	assert.Equal(t, 2, rec.calls, "null chronometric id groups by own id: each runs")
}

func (s *outboxSuite) TestUnknownKindFails() {
	t := s.T()
	s.enqueue(Request{Kind: "ghost", Payload: map[string]any{}})
	rec := &recorder{}
	s.worker(rec, 3).processBatch(context.Background())
	assert.Equal(t, 0, rec.calls)
	status, _, lastErr, evac := s.rowState("ghost")
	assert.Equal(t, "failed", status)
	assert.Contains(t, lastErr, "no handler")
	assert.True(t, evac)
}

func (s *outboxSuite) TestCleanupDeletesExpiredTerminal() {
	t := s.T()
	ctx := context.Background()
	old := time.Now().Add(-100 * time.Hour)

	s.enqueue(Request{Kind: "old_done"})
	_, err := s.Pool.Exec(ctx, `UPDATE outbox_tasks SET status='done', updated_at=$1 WHERE kind='old_done'`, old)
	require.NoError(t, err)
	s.enqueue(Request{Kind: "old_failed"})
	_, err = s.Pool.Exec(ctx, `UPDATE outbox_tasks SET status='failed', evacuated_at=$1 WHERE kind='old_failed'`, old)
	require.NoError(t, err)
	s.enqueue(Request{Kind: "fresh_done"})
	_, err = s.Pool.Exec(ctx, `UPDATE outbox_tasks SET status='done', updated_at=$1 WHERE kind='fresh_done'`, time.Now())
	require.NoError(t, err)
	s.enqueue(Request{Kind: "still_pending"})

	st := &store{pool: s.Pool}
	now := time.Now()
	n, err := st.cleanup(ctx, now.Add(-72*time.Hour), now.Add(-240*time.Hour))
	require.NoError(t, err)
	// old_done (100h > 72h done TTL) deleted; old_failed (100h < 240h failed TTL) kept;
	// fresh_done kept; pending kept.
	assert.Equal(t, int64(1), n)
	assert.Equal(t, 3, s.count())
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	assert.Equal(t, backoffBase, backoff(1))
	assert.Equal(t, backoffBase*2, backoff(2))
	assert.Equal(t, backoffBase*4, backoff(3))
	assert.Equal(t, backoffMax, backoff(100), "capped")
}
