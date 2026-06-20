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

func (r *pgRepository) SetRoles(ctx context.Context, serverID uuid.UUID, command string, roleIDs []string, createdByUserID string) error {
	// Replace the command's role set atomically: drop roles no longer desired,
	// then insert the desired ones. An empty roleIDs clears the command, because
	// `role_id <> ALL('{}')` is true for every row. created_at is supplied by the
	// app in the configured timezone (the column has no DB default); the insert is
	// idempotent (a kept role hits the unique constraint and is a no-op).
	// A nil slice encodes as SQL NULL (and `role_id <> ALL(NULL)` is NULL, so it
	// would delete nothing); an empty non-nil slice encodes as '{}', which clears.
	if roleIDs == nil {
		roleIDs = []string{}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed; rolls back on early return

	if _, err := tx.Exec(ctx,
		`DELETE FROM permissions WHERE servers_id = $1 AND command = $2 AND role_id <> ALL($3)`,
		serverID, command, roleIDs); err != nil {
		return err
	}
	now := time.Now()
	for _, roleID := range roleIDs {
		id, err := uuidv7.New()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO permissions (id, servers_id, command, role_id, created_by_user_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (servers_id, command, role_id) DO NOTHING`,
			id, serverID, command, roleID, createdByUserID, now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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
