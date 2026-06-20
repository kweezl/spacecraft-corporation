package outbox

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kweezl/spacecraft-corporation/internal/uuidv7"
)

// enqueuer implements Enqueuer. It is stateless — Enqueue runs entirely on the
// caller's transaction.
type enqueuer struct{}

// NewEnqueuer returns the Enqueuer. Exported so feature repositories (and their
// tests) can enqueue on their own transactions; the fx module provides it.
func NewEnqueuer() Enqueuer { return enqueuer{} }

func (enqueuer) Enqueue(ctx context.Context, tx pgx.Tx, req Request) error {
	id, err := uuidv7.NewUUID()
	if err != nil {
		return err
	}
	payload, err := marshalPayload(req.Payload)
	if err != nil {
		return err
	}
	version := req.Version
	if version == 0 {
		version = 1
	}
	now := time.Now()
	nextTry := now.Add(req.Delay)
	var chrono *uuid.UUID
	if req.ChronometricID != uuid.Nil {
		chrono = &req.ChronometricID
	}
	// Each enqueue is a durable row (no coalescing here); the worker collapses by
	// (kind, chronometric_id) at run time.
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_tasks
			(id, kind, version, payload, chronometric_id, status, attempts, last_error, next_try_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'pending', 0, '', $6, $7, $7)`,
		id, req.Kind, version, payload, chrono, nextTry, now)
	return err
}

// store is the worker's view of the table.
type store struct {
	pool *pgxpool.Pool
}

// dueTask is a pending task ready to run, with its collapsing group.
type dueTask struct {
	Task
	chronometric *uuid.UUID
}

// group is the (kind, chronometric_id) collapsing key; a NULL chronometric id
// groups by the task's own id, so it never collapses with another row.
func (d dueTask) group() string {
	if d.chronometric != nil {
		return d.Kind + "|" + d.chronometric.String()
	}
	return d.Kind + "|" + d.ID.String()
}

// due returns the pending tasks whose next_try_at has arrived, oldest first.
// There is no lease/lock: the worker is a single goroutine in a single process,
// so a selected task can't be re-selected until it is marked terminal or
// rescheduled. (Multi-instance would need SELECT ... FOR UPDATE SKIP LOCKED.)
func (s *store) due(ctx context.Context, now time.Time, limit int) ([]dueTask, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, kind, version, payload, attempts, chronometric_id
		FROM outbox_tasks
		WHERE status = 'pending' AND next_try_at <= $1
		ORDER BY id
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dueTask
	for rows.Next() {
		var d dueTask
		if err := rows.Scan(&d.ID, &d.Kind, &d.Version, &d.Payload, &d.Attempts, &d.chronometric); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *store) markDone(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE outbox_tasks SET status = 'done', updated_at = $1 WHERE id = $2`, time.Now(), id)
	return err
}

// markRetry records the error and reschedules the task; attempts is the new count.
func (s *store) markRetry(ctx context.Context, id uuid.UUID, attempts int, lastErr string, nextTryAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE outbox_tasks SET status = 'pending', attempts = $1, last_error = $2,
		       next_try_at = $3, updated_at = $4 WHERE id = $5`,
		attempts, lastErr, nextTryAt, time.Now(), id)
	return err
}

// markFailed abandons a task: status failed + evacuated_at stamped (the give-up
// anchor for retention).
func (s *store) markFailed(ctx context.Context, id uuid.UUID, attempts int, lastErr string) error {
	now := time.Now()
	_, err := s.pool.Exec(ctx, `
		UPDATE outbox_tasks SET status = 'failed', attempts = $1, last_error = $2,
		       evacuated_at = $3, updated_at = $3 WHERE id = $4`,
		attempts, lastErr, now, id)
	return err
}

// markSuperseded retires an older task in a collapsing group without running it
// (a newer task in the same (kind, chronometric_id) will perform the effect).
func (s *store) markSuperseded(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE outbox_tasks SET status = 'done', updated_at = $1 WHERE id = $2 AND status = 'pending'`,
		time.Now(), id)
	return err
}

// cleanup deletes terminal tasks past their retention: done older than
// doneCutoff (by updated_at), failed older than failedCutoff (by evacuated_at).
// Returns the number of rows removed.
func (s *store) cleanup(ctx context.Context, doneCutoff, failedCutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM outbox_tasks
		WHERE (status = 'done' AND updated_at < $1)
		   OR (status = 'failed' AND evacuated_at < $2)`,
		doneCutoff, failedCutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
