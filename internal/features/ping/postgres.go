package ping

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepository struct {
	pool *pgxpool.Pool
}

func newRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

func (r *pgRepository) Record(ctx context.Context, serverID uuid.UUID, userID string) error {
	// serverID is the resolved servers.id (servers_id FK). created_at is
	// app-supplied in the configured timezone (no DB default).
	_, err := r.pool.Exec(ctx,
		`INSERT INTO ping_log (servers_id, user_id, created_at) VALUES ($1, $2, $3)`,
		serverID, userID, time.Now())
	return err
}

func (r *pgRepository) Count(ctx context.Context, serverID uuid.UUID) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM ping_log WHERE servers_id = $1`, serverID).Scan(&n)
	return n, err
}
