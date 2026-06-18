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

func (r *pgRepository) Record(ctx context.Context, guildID, userID string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO ping_log (guild_id, user_id) VALUES ($1, $2)`, guildID, userID)
	return err
}

func (r *pgRepository) Count(ctx context.Context, guildID string) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM ping_log WHERE guild_id = $1`, guildID).Scan(&n)
	return n, err
}
