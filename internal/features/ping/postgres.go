package ping

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepository struct {
	pool *pgxpool.Pool
}

func newRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

func (r *pgRepository) Record(ctx context.Context, serverID, userID string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO ping_log (server_id, user_id) VALUES ($1, $2)`, serverID, userID)
	return err
}

func (r *pgRepository) Count(ctx context.Context, serverID string) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM ping_log WHERE server_id = $1`, serverID).Scan(&n)
	return n, err
}
