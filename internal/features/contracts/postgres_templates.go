package contracts

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/kweezl/spacecraft-corporation/internal/outbox"
	"github.com/kweezl/spacecraft-corporation/internal/uuidv7"
)

// The TemplateRepository implementation, on the same pgRepository as Repository.
// Templates have no forum post, so unlike the contract mutations nothing here
// touches the outbox — deletes and edits are plain transactions.

func newTemplateRepository(pool *pgxpool.Pool, enq outbox.Enqueuer) TemplateRepository {
	return &pgRepository{pool: pool, enq: enq}
}

// uniqueViolation reports whether err is the Postgres unique-constraint error —
// the race-safe backstop behind the friendly EXISTS pre-checks (templates have
// no parent row to lock during create, so a pre-check alone can race).
func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// lockTemplate resolves a template by id + server and takes a row lock so
// concurrent template mutations serialize. ErrTemplateNotFound when absent.
func lockTemplate(ctx context.Context, tx pgx.Tx, serverID, templateID uuid.UUID) error {
	var id uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM contract_templates WHERE id = $1 AND servers_id = $2 FOR UPDATE`,
		templateID, serverID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTemplateNotFound
	}
	return err
}

// lockTemplateByItem resolves the template owning a template item (scoped to the
// server) and locks the template row. A forged/cross-server item id yields
// ErrTemplateItemNotFound. Returns the template id.
func lockTemplateByItem(ctx context.Context, tx pgx.Tx, serverID, templateItemID uuid.UUID) (uuid.UUID, error) {
	var tid uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT t.id FROM contract_template_items ti JOIN contract_templates t ON t.id = ti.contract_templates_id
		 WHERE ti.id = $1 AND t.servers_id = $2 FOR UPDATE OF t`,
		templateItemID, serverID).Scan(&tid)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrTemplateItemNotFound
	}
	return tid, err
}

// touchTemplate advances a template's updated_at/updated_by within a transaction.
func touchTemplate(ctx context.Context, tx pgx.Tx, templateID uuid.UUID, now time.Time, actor string) error {
	_, err := tx.Exec(ctx,
		`UPDATE contract_templates SET updated_at = $1, updated_by_user_id = $2 WHERE id = $3`,
		now, actor, templateID)
	return err
}

func (r *pgRepository) CreateTemplate(ctx context.Context, serverID uuid.UUID, title, description string, factor decimal.Decimal, actor string) (uuid.UUID, error) {
	id, err := uuidv7.NewUUID()
	if err != nil {
		return uuid.Nil, err
	}
	now := time.Now()
	// Zero-value defaults for everything the create modal doesn't ask (rewards,
	// duration, location) — filled in afterward on the template page. The
	// participant reward factor is the exception: the caller prefills it from
	// the server default.
	_, err = r.pool.Exec(ctx, `
		INSERT INTO contract_templates
			(id, servers_id, title, description,
			 reward_corpo_credits, reward_corpo_reputation, reward_corpo_licence_points, participant_reward_factor, deadline_minutes,
			 delivery_location_gdid, delivery_location_gd_version,
			 created_by_user_id, updated_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 0, 0, 0, $5, 0, NULL, NULL, $6, $6, $7, $7)`,
		id, serverID, title, description, factor, actor, now)
	if uniqueViolation(err) {
		return uuid.Nil, ErrTemplateExists
	}
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (r *pgRepository) TemplateByID(ctx context.Context, serverID, templateID uuid.UUID) (Template, error) {
	var t Template
	err := r.pool.QueryRow(ctx, `
		SELECT id, servers_id, title, description,
		       reward_corpo_credits, reward_corpo_reputation, reward_corpo_licence_points, participant_reward_factor, deadline_minutes,
		       COALESCE(delivery_location_gdid, ''), COALESCE(delivery_location_gd_version, ''), created_by_user_id
		FROM contract_templates WHERE id = $1 AND servers_id = $2`,
		templateID, serverID).
		Scan(&t.ID, &t.ServerID, &t.Title, &t.Description,
			&t.RewardCredits, &t.RewardReputation, &t.RewardLicencePoints, &t.ParticipantRewardFactor, &t.DeadlineMinutes,
			&t.LocationGDID, &t.LocationGDVersion, &t.CreatedByUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrTemplateNotFound
	}
	if err != nil {
		return Template{}, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, item_gdid, gamedata_version, quantity
		FROM contract_template_items WHERE contract_templates_id = $1
		ORDER BY created_at, id`, t.ID)
	if err != nil {
		return Template{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var it TemplateItem
		if err := rows.Scan(&it.ID, &it.GDID, &it.GDVersion, &it.Qty); err != nil {
			return Template{}, err
		}
		t.Items = append(t.Items, it)
	}
	return t, rows.Err()
}

// escapeLike makes a user query safe inside an ILIKE pattern: the metacharacters
// %, _ and the escape char itself match literally.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}

func (r *pgRepository) ListTemplates(ctx context.Context, serverID uuid.UUID, titleQuery string, limit, offset int) ([]TemplateListEntry, int, error) {
	pattern := "%" + escapeLike(strings.TrimSpace(titleQuery)) + "%"

	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM contract_templates WHERE servers_id = $1 AND title ILIKE $2`,
		serverID, pattern).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.title,
		       (SELECT count(*) FROM contract_template_items ti WHERE ti.contract_templates_id = t.id)
		FROM contract_templates t
		WHERE t.servers_id = $1 AND t.title ILIKE $2
		ORDER BY lower(t.title), t.id LIMIT $3 OFFSET $4`,
		serverID, pattern, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var page []TemplateListEntry
	for rows.Next() {
		var e TemplateListEntry
		if err := rows.Scan(&e.ID, &e.Title, &e.ItemCount); err != nil {
			return nil, 0, err
		}
		page = append(page, e)
	}
	return page, total, rows.Err()
}

func (r *pgRepository) UpdateTemplateDetails(ctx context.Context, serverID, templateID uuid.UUID, title, description string, deadlineMinutes int, actor string) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockTemplate(ctx, tx, serverID, templateID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`UPDATE contract_templates SET title = $1, description = $2, deadline_minutes = $3,
		 updated_at = $4, updated_by_user_id = $5 WHERE id = $6`,
		title, description, deadlineMinutes, now, actor, templateID)
	if uniqueViolation(err) {
		return ErrTemplateExists
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) UpdateTemplateRewards(ctx context.Context, serverID, templateID uuid.UUID, credits, factor decimal.Decimal, reputation, licencePoints int, actor string) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockTemplate(ctx, tx, serverID, templateID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE contract_templates SET reward_corpo_credits = $1, reward_corpo_reputation = $2, reward_corpo_licence_points = $3, participant_reward_factor = $4,
		 updated_at = $5, updated_by_user_id = $6 WHERE id = $7`,
		credits, reputation, licencePoints, factor, now, actor, templateID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) SetTemplateLocation(ctx context.Context, serverID, templateID uuid.UUID, gdid, gdVersion, actor string) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockTemplate(ctx, tx, serverID, templateID); err != nil {
		return err
	}
	if gdid == "" {
		gdVersion = ""
	}
	if _, err := tx.Exec(ctx,
		`UPDATE contract_templates SET delivery_location_gdid = $1, delivery_location_gd_version = $2,
		 updated_at = $3, updated_by_user_id = $4 WHERE id = $5`,
		nullIfEmpty(gdid), nullIfEmpty(gdVersion), now, actor, templateID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) AddTemplateItem(ctx context.Context, serverID, templateID uuid.UUID, gdid, gdVersion string, qty, maxItems int, actor string) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockTemplate(ctx, tx, serverID, templateID); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM contract_template_items WHERE contract_templates_id = $1`, templateID).Scan(&count); err != nil {
		return err
	}
	if count >= maxItems {
		return ErrMaxItems
	}
	// The template row is locked, so the existence check is race-free.
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM contract_template_items WHERE contract_templates_id = $1 AND item_gdid = $2)`,
		templateID, gdid).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrTemplateItemExists
	}

	id, err := uuidv7.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO contract_template_items
			(id, contract_templates_id, item_gdid, gamedata_version, quantity,
			 created_by_user_id, updated_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6, $7, $7)`,
		id, templateID, gdid, gdVersion, qty, actor, now); err != nil {
		return err
	}
	if err := touchTemplate(ctx, tx, templateID, now, actor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) UpdateTemplateItemQty(ctx context.Context, serverID, templateItemID uuid.UUID, qty int, actor string) (uuid.UUID, error) {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tid, err := lockTemplateByItem(ctx, tx, serverID, templateItemID)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE contract_template_items SET quantity = $1, updated_by_user_id = $2, updated_at = $3 WHERE id = $4`,
		qty, actor, now, templateItemID); err != nil {
		return uuid.Nil, err
	}
	if err := touchTemplate(ctx, tx, tid, now, actor); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return tid, nil
}

func (r *pgRepository) RemoveTemplateItem(ctx context.Context, serverID, templateItemID uuid.UUID, actor string) (uuid.UUID, error) {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tid, err := lockTemplateByItem(ctx, tx, serverID, templateItemID)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM contract_template_items WHERE id = $1`, templateItemID); err != nil {
		return uuid.Nil, err
	}
	if err := touchTemplate(ctx, tx, tid, now, actor); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return tid, nil
}

// The actor is accepted for interface symmetry but unrecorded — a delete leaves
// no row to audit.
func (r *pgRepository) DeleteTemplate(ctx context.Context, serverID, templateID uuid.UUID, _ string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockTemplate(ctx, tx, serverID, templateID); err != nil {
		return err
	}
	// Items first (FK is RESTRICT); contracts created from the template keep their
	// copied values — their provenance FK nulls itself (ON DELETE SET NULL).
	if _, err := tx.Exec(ctx,
		`DELETE FROM contract_template_items WHERE contract_templates_id = $1`, templateID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM contract_templates WHERE id = $1`, templateID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
