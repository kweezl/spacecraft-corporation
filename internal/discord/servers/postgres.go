package servers

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepository struct {
	pool *pgxpool.Pool
}

func newRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

// newID returns a fresh UUIDv7 as a string. v7 embeds a timestamp, so IDs sort
// in creation order — friendlier for indexes and pagination than random v4. The
// application owns ID generation; the DB columns have no DEFAULT.
func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("servers: generate uuid: %w", err)
	}
	return id.String(), nil
}

// Upsert detects insert-vs-update in one round trip via the system column xmax:
// it is 0 for a freshly inserted row and non-zero for a row touched by an
// UPDATE, so `(xmax = 0)` is true exactly when a new row was inserted. The
// approval clause `servers.approved OR EXCLUDED.approved` only ever promotes to
// true, so a manual approval is never overwritten by a server falling out of
// the allowlist.
func (r *pgRepository) Upsert(ctx context.Context, serverID, name string, inList bool) (bool, error) {
	id, err := newID()
	if err != nil {
		return false, err
	}
	var inserted bool
	err = r.pool.QueryRow(ctx, `
		INSERT INTO servers (id, server_id, name, approved)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (server_id) DO UPDATE
		SET name       = EXCLUDED.name,
		    approved   = servers.approved OR EXCLUDED.approved,
		    updated_at = now()
		RETURNING (xmax = 0)`, id, serverID, name, inList).Scan(&inserted)
	return inserted, err
}

func (r *pgRepository) LogEvent(ctx context.Context, serverID, eventType string) error {
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO server_event (id, server_id, event_type) VALUES ($1, $2, $3)`,
		id, serverID, eventType)
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
