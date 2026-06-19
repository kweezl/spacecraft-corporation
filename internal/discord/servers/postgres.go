package servers

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	var inserted bool
	err := r.pool.QueryRow(ctx, `
		INSERT INTO servers (server_id, name, approved)
		VALUES ($1, $2, $3)
		ON CONFLICT (server_id) DO UPDATE
		SET name       = EXCLUDED.name,
		    approved   = servers.approved OR EXCLUDED.approved,
		    updated_at = now()
		RETURNING (xmax = 0)`, serverID, name, inList).Scan(&inserted)
	return inserted, err
}

func (r *pgRepository) LogEvent(ctx context.Context, serverID, eventType string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO server_event (server_id, event_type) VALUES ($1, $2)`, serverID, eventType)
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
