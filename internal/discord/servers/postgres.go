package servers

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kweezl/spacecraft-corporation/internal/uuidv7"
)

type pgRepository struct {
	pool *pgxpool.Pool
}

func newRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

// Upsert detects insert-vs-update in one round trip via the system column xmax:
// it is 0 for a freshly inserted row and non-zero for a row touched by an
// UPDATE, so `(xmax = 0)` is true exactly when a new row was inserted. The
// approval clause `servers.approved OR EXCLUDED.approved` only ever promotes to
// true, so a manual approval is never overwritten by a server falling out of
// the allowlist.
func (r *pgRepository) Upsert(ctx context.Context, serverID, name string, inList bool) (bool, error) {
	id, err := uuidv7.New()
	if err != nil {
		return false, err
	}
	// created_at/updated_at are app-supplied in the configured timezone (no DB
	// default); on update only updated_at advances, created_at is preserved.
	now := time.Now()
	var inserted bool
	err = r.pool.QueryRow(ctx, `
		INSERT INTO servers (id, server_id, name, approved, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (server_id) DO UPDATE
		SET name       = EXCLUDED.name,
		    approved   = servers.approved OR EXCLUDED.approved,
		    updated_at = EXCLUDED.updated_at
		RETURNING (xmax = 0)`, id, serverID, name, inList, now).Scan(&inserted)
	return inserted, err
}

func (r *pgRepository) LogEvent(ctx context.Context, serverID, eventType string) error {
	id, err := uuidv7.New()
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO server_event (id, server_id, event_type, created_at) VALUES ($1, $2, $3, $4)`,
		id, serverID, eventType, time.Now())
	return err
}

func (r *pgRepository) IsApproved(ctx context.Context, serverID string) (bool, error) {
	var approved bool
	err := r.pool.QueryRow(ctx,
		`SELECT approved FROM servers WHERE server_id = $1`, serverID).Scan(&approved)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return approved, err
}
