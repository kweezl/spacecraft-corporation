package settings

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/uuidv7"
)

type pgRepository struct {
	pool *pgxpool.Pool
}

func newRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

func (r *pgRepository) Get(ctx context.Context, serverID uuid.UUID) (Settings, error) {
	var s Settings
	var language string // scan into a plain string; i18n.Language is a named type
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(theme, ''), COALESCE(language, ''), COALESCE(contracts_forum_channel_id, '')
		 FROM server_settings WHERE servers_id = $1`,
		serverID).Scan(&s.Theme, &language, &s.ContractsForumChannelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, nil
	}
	s.Language = i18n.Language(language)
	return s, err
}

func (r *pgRepository) SetTheme(ctx context.Context, serverID uuid.UUID, theme string) error {
	return r.upsert(ctx, serverID, "theme", theme)
}

func (r *pgRepository) SetLanguage(ctx context.Context, serverID uuid.UUID, language i18n.Language) error {
	return r.upsert(ctx, serverID, "language", string(language))
}

func (r *pgRepository) SetContractsForumChannelID(ctx context.Context, serverID uuid.UUID, channelID string) error {
	return r.upsert(ctx, serverID, "contracts_forum_channel_id", channelID)
}

// upsert sets one column (theme or language) for a server, inserting the row if
// absent. column is a trusted constant (never user input). id and timestamps are
// app-supplied (no DB defaults); on update only the column and updated_at change.
func (r *pgRepository) upsert(ctx context.Context, serverID uuid.UUID, column, value string) error {
	id, err := uuidv7.New()
	if err != nil {
		return err
	}
	now := time.Now()
	// serverID is the resolved servers.id (servers_id FK).
	// #nosec G201 -- column is a hardcoded constant ("theme"/"language"), not input.
	sql := `
		INSERT INTO server_settings (id, servers_id, ` + column + `, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (servers_id) DO UPDATE
		SET ` + column + ` = EXCLUDED.` + column + `, updated_at = EXCLUDED.updated_at`
	_, err = r.pool.Exec(ctx, sql, id, serverID, value, now)
	return err
}
