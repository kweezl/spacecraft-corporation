package ping

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepository struct {
	pool *pgxpool.Pool
}

func newRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

func (r *pgRepository) Record(ctx context.Context, serverID, userID string) error {
	// servers_id references servers.id; resolve the Discord snowflake to it in
	// SQL. created_at is app-supplied in the configured timezone (no DB default).
	_, err := r.pool.Exec(ctx,
		`INSERT INTO ping_log (servers_id, user_id, created_at)
		 VALUES ((SELECT id FROM servers WHERE server_id = $1), $2, $3)`,
		serverID, userID, time.Now())
	return err
}

func (r *pgRepository) Count(ctx context.Context, serverID string) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM ping_log WHERE servers_id = (SELECT id FROM servers WHERE server_id = $1)`,
		serverID).Scan(&n)
	return n, err
}
