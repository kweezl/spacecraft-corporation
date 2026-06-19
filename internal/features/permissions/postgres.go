package permissions

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kweezl/spacecraft-corporation/internal/uuidv7"
)

type pgRepository struct {
	pool *pgxpool.Pool
}

func newRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

func (r *pgRepository) RolesFor(ctx context.Context, serverID uuid.UUID, command string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT role_id FROM permissions WHERE servers_id = $1 AND command = $2`,
		serverID, command)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []string
	for rows.Next() {
		var roleID string
		if err := rows.Scan(&roleID); err != nil {
			return nil, err
		}
		roles = append(roles, roleID)
	}
	return roles, rows.Err()
}

func (r *pgRepository) Grant(ctx context.Context, serverID uuid.UUID, command, roleID, createdByUserID string) error {
	id, err := uuidv7.New()
	if err != nil {
		return err
	}
	// created_at is supplied by the app in the configured timezone (the column
	// has no DB default). Idempotent: a repeated grant hits the
	// (server_id, command, role_id) unique constraint and is a no-op.
	_, err = r.pool.Exec(ctx, `
		INSERT INTO permissions (id, servers_id, command, role_id, created_by_user_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (servers_id, command, role_id) DO NOTHING`,
		id, serverID, command, roleID, createdByUserID, time.Now())
	return err
}

func (r *pgRepository) Revoke(ctx context.Context, serverID uuid.UUID, command, roleID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM permissions WHERE servers_id = $1 AND command = $2 AND role_id = $3`,
		serverID, command, roleID)
	return err
}

func (r *pgRepository) Clear(ctx context.Context, serverID uuid.UUID, command string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM permissions WHERE servers_id = $1 AND command = $2`,
		serverID, command)
	return err
}

func (r *pgRepository) List(ctx context.Context, serverID uuid.UUID) ([]Mapping, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT command, role_id FROM permissions
		 WHERE servers_id = $1 ORDER BY command, role_id`,
		serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Mapping
	for rows.Next() {
		var m Mapping
		if err := rows.Scan(&m.Command, &m.RoleID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
