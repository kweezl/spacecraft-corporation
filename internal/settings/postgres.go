package settings

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

func (r *pgRepository) Get(ctx context.Context, serverID string) (Settings, error) {
	var s Settings
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(theme, ''), COALESCE(language, '') FROM server_settings
		 WHERE servers_id = (SELECT id FROM servers WHERE server_id = $1)`,
		serverID).Scan(&s.Theme, &s.Language)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, nil
	}
	return s, err
}

func (r *pgRepository) SetTheme(ctx context.Context, serverID, theme string) error {
	return r.upsert(ctx, serverID, "theme", theme)
}

func (r *pgRepository) SetLanguage(ctx context.Context, serverID, language string) error {
	return r.upsert(ctx, serverID, "language", language)
}

// upsert sets one column (theme or language) for a server, inserting the row if
// absent. column is a trusted constant (never user input). id and timestamps are
// app-supplied (no DB defaults); on update only the column and updated_at change.
func (r *pgRepository) upsert(ctx context.Context, serverID, column, value string) error {
	id, err := uuidv7.New()
	if err != nil {
		return err
	}
	now := time.Now()
	// servers_id references servers.id; resolve the Discord snowflake to it in SQL.
	// #nosec G201 -- column is a hardcoded constant ("theme"/"language"), not input.
	sql := `
		INSERT INTO server_settings (id, servers_id, ` + column + `, created_at, updated_at)
		VALUES ($1, (SELECT id FROM servers WHERE server_id = $2), $3, $4, $4)
		ON CONFLICT (servers_id) DO UPDATE
		SET ` + column + ` = EXCLUDED.` + column + `, updated_at = EXCLUDED.updated_at`
	_, err = r.pool.Exec(ctx, sql, id, serverID, value, now)
	return err
}
