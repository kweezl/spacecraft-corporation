package settings

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

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
		`SELECT COALESCE(theme, ''), COALESCE(language, ''), COALESCE(contracts_forum_channel_id, ''),
		        COALESCE(contracts_reports_channel_id, ''), COALESCE(contracts_participant_reward_factor, 0),
		        COALESCE(supply_forum_channel_id, ''), supply_request_limit, contracts_max_items
		 FROM server_settings WHERE servers_id = $1`,
		serverID).Scan(&s.Theme, &language, &s.ContractsForumChannelID, &s.ContractsReportsChannelID, &s.ContractsRewardFactor,
		&s.SupplyForumChannelID, &s.SupplyRequestLimit, &s.ContractsMaxItems)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, nil
	}
	s.Language = i18n.Language(language)
	// Canonicalize an unset/zero factor to the Decimal zero value: a scanned
	// NUMERIC 0 carries a different internal representation (allocated big.Int),
	// which would make otherwise-equal Settings values compare unequal.
	if s.ContractsRewardFactor.IsZero() {
		s.ContractsRewardFactor = decimal.Decimal{}
	}
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

func (r *pgRepository) SetContractsReportsChannelID(ctx context.Context, serverID uuid.UUID, channelID string) error {
	return r.upsert(ctx, serverID, "contracts_reports_channel_id", channelID)
}

func (r *pgRepository) SetContractsRewardFactor(ctx context.Context, serverID uuid.UUID, factor decimal.Decimal) error {
	return r.upsert(ctx, serverID, "contracts_participant_reward_factor", factor)
}

func (r *pgRepository) SetSupplyForumChannelID(ctx context.Context, serverID uuid.UUID, channelID string) error {
	return r.upsert(ctx, serverID, "supply_forum_channel_id", channelID)
}

func (r *pgRepository) SetSupplyRequestLimit(ctx context.Context, serverID uuid.UUID, limit int) error {
	return r.upsert(ctx, serverID, "supply_request_limit", limit)
}

func (r *pgRepository) SetContractsMaxItems(ctx context.Context, serverID uuid.UUID, limit int) error {
	return r.upsert(ctx, serverID, "contracts_max_items", limit)
}

// upsert sets one column for a server, inserting the row if absent. column is a
// trusted constant (never user input); value is bound as a parameter (string or
// decimal — pgx encodes both). id and timestamps are app-supplied (no DB
// defaults); on update only the column and updated_at change.
func (r *pgRepository) upsert(ctx context.Context, serverID uuid.UUID, column string, value any) error {
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
