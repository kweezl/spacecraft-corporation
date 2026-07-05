package supply

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kweezl/spacecraft-corporation/internal/outbox"
	"github.com/kweezl/spacecraft-corporation/internal/uuidv7"
)

type pgRepository struct {
	pool *pgxpool.Pool
	enq  outbox.Enqueuer
}

func newRepository(pool *pgxpool.Pool, enq outbox.Enqueuer) Repository {
	return &pgRepository{pool: pool, enq: enq}
}

// --- helpers -----------------------------------------------------------------

// nullIfEmpty maps "" to a NULL bind so an unset optional column stores NULL.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// asLocal reinterprets a TIMESTAMP value (pgx decodes it UTC-labeled, carrying
// the stored wall-clock numbers) as the configured local zone, so durations and
// Discord timestamps come out right. Mirrors the contracts repository.
func asLocal(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
}

func (r *pgRepository) enqueueRefresh(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	return r.enq.Enqueue(ctx, tx, outbox.Request{Kind: taskRefresh, Payload: taskPayload{RequestID: id}, ChronometricID: id})
}

func (r *pgRepository) enqueueClose(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	return r.enq.Enqueue(ctx, tx, outbox.Request{Kind: taskClose, Payload: taskPayload{RequestID: id}, ChronometricID: id})
}

// touch advances a request's updated_at within a transaction. Every caller
// enqueues a refresh in the same tx.
func touch(ctx context.Context, tx pgx.Tx, id uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE supply_requests SET updated_at = $1 WHERE id = $2`, now, id)
	return err
}

// requestCols is the SELECT list for a request row, in scanRequest order.
const requestCols = `id, servers_id, owner_user_id, COALESCE(thread_id, ''), title, description, status, post_version,
	COALESCE(delivery_location_gdid, ''), COALESCE(delivery_location_gd_version, ''),
	COALESCE(system_name, ''), COALESCE(system_code, ''), planet_number,
	ref_message_guild_id, ref_message_channel_id, ref_message_id, created_at, updated_at`

func scanRequest(row pgx.Row, req *Request) error {
	var (
		status           string
		planet           *int
		refG, refC, refM *string
	)
	if err := row.Scan(&req.ID, &req.ServerID, &req.OwnerUserID, &req.ThreadID, &req.Title, &req.Description,
		&status, &req.PostVersion, &req.LocationGDID, &req.LocationGDVersion, &req.SystemName, &req.SystemCode,
		&planet, &refG, &refC, &refM, &req.CreatedAt, &req.UpdatedAt); err != nil {
		return err
	}
	req.Status = Status(status)
	req.PlanetNumber = planet
	if refG != nil && refC != nil && refM != nil {
		req.RefMessage = &MessageRef{GuildID: *refG, ChannelID: *refC, MessageID: *refM}
	}
	req.CreatedAt = asLocal(req.CreatedAt)
	req.UpdatedAt = asLocal(req.UpdatedAt)
	return nil
}

// loadByID loads a request and its items by id.
func (r *pgRepository) loadByID(ctx context.Context, id uuid.UUID) (Progress, error) {
	var p Progress
	row := r.pool.QueryRow(ctx, `SELECT `+requestCols+` FROM supply_requests WHERE id = $1`, id)
	if err := scanRequest(row, &p.Request); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Progress{}, ErrNotFound
		}
		return Progress{}, err
	}
	items, err := r.loadItems(ctx, id)
	if err != nil {
		return Progress{}, err
	}
	p.Items = items
	return p, nil
}

// loadItems loads a request's items with their reserved/delivered aggregates and
// per-member participant breakdown (ordered by add time, then user).
func (r *pgRepository) loadItems(ctx context.Context, requestID uuid.UUID) ([]Item, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT i.id, i.item_gdid, i.gamedata_version, i.required_qty,
		       COALESCE(SUM(res.reserved_qty), 0), COALESCE(SUM(res.delivered_qty), 0)
		FROM supply_request_items i
		LEFT JOIN supply_reservations res ON res.supply_request_items_id = i.id
		WHERE i.supply_requests_id = $1
		GROUP BY i.id
		ORDER BY i.created_at, i.id`, requestID)
	if err != nil {
		return nil, err
	}
	var items []Item
	byID := map[uuid.UUID]*Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.GDID, &it.GDVersion, &it.RequiredQty, &it.ReservedQty, &it.DeliveredQty); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for idx := range items {
		byID[items[idx].ID] = &items[idx]
	}
	if len(items) == 0 {
		return items, nil
	}

	pRows, err := r.pool.Query(ctx, `
		SELECT res.supply_request_items_id, res.user_id, res.reserved_qty, res.delivered_qty
		FROM supply_reservations res
		JOIN supply_request_items i ON i.id = res.supply_request_items_id
		WHERE i.supply_requests_id = $1
		ORDER BY res.user_id`, requestID)
	if err != nil {
		return nil, err
	}
	defer pRows.Close()
	for pRows.Next() {
		var (
			itemID uuid.UUID
			part   Participant
		)
		if err := pRows.Scan(&itemID, &part.UserID, &part.Reserved, &part.Delivered); err != nil {
			return nil, err
		}
		if it := byID[itemID]; it != nil {
			it.Participants = append(it.Participants, part)
		}
	}
	return items, pRows.Err()
}

// --- lock helpers ------------------------------------------------------------

// lockOpenByThread resolves an open request by (server, thread) and row-locks it.
func lockOpenByThread(ctx context.Context, tx pgx.Tx, serverID uuid.UUID, threadID string) (uuid.UUID, error) {
	var id uuid.UUID
	var status string
	err := tx.QueryRow(ctx,
		`SELECT id, status FROM supply_requests WHERE servers_id = $1 AND thread_id = $2 FOR UPDATE`,
		serverID, threadID).Scan(&id, &status)
	return checkOpen(id, status, err)
}

// lockOpenByOwner resolves an open request by (id, server, owner) and locks it.
func lockOpenByOwner(ctx context.Context, tx pgx.Tx, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID) (uuid.UUID, error) {
	var status string
	err := tx.QueryRow(ctx,
		`SELECT status FROM supply_requests WHERE id = $1 AND servers_id = $2 AND owner_user_id = $3 FOR UPDATE`,
		requestID, serverID, ownerUserID).Scan(&status)
	return checkOpen(requestID, status, err)
}

// lockOpenByItemOwner resolves the open request owning itemID, scoped to (server,
// owner), and locks the request row. A forged/cross-owner item id → ErrNotFound.
func lockOpenByItemOwner(ctx context.Context, tx pgx.Tx, serverID uuid.UUID, ownerUserID string, itemID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	var status string
	err := tx.QueryRow(ctx, `
		SELECT req.id, req.status FROM supply_request_items i
		JOIN supply_requests req ON req.id = i.supply_requests_id
		WHERE i.id = $1 AND req.servers_id = $2 AND req.owner_user_id = $3 FOR UPDATE OF req`,
		itemID, serverID, ownerUserID).Scan(&id, &status)
	return checkOpen(id, status, err)
}

// checkOpen maps a lock-query result to (id, error): no row → ErrNotFound,
// non-open status → ErrClosed.
func checkOpen(id uuid.UUID, status string, err error) (uuid.UUID, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if Status(status) != StatusOpen {
		return uuid.Nil, ErrClosed
	}
	return id, nil
}

// lockItemByGDID resolves an item by (request, gdid) and locks it.
func lockItemByGDID(ctx context.Context, tx pgx.Tx, requestID uuid.UUID, gdid string) (id uuid.UUID, required int, err error) {
	err = tx.QueryRow(ctx,
		`SELECT id, required_qty FROM supply_request_items WHERE supply_requests_id = $1 AND item_gdid = $2 FOR UPDATE`,
		requestID, gdid).Scan(&id, &required)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, 0, ErrItemNotFound
	}
	return id, required, err
}

// --- create ------------------------------------------------------------------

func (r *pgRepository) Create(ctx context.Context, in CreateInput) (uuid.UUID, error) {
	id, err := uuidv7.NewUUID()
	if err != nil {
		return uuid.Nil, err
	}
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize concurrent creates for this (server, owner) so the open-request
	// limit can't be raced past: a plain count+insert under READ COMMITTED is not
	// safe (two txns each see the committed N-1, both pass the check, both insert
	// → N+1). A transaction-scoped advisory lock keyed on the pair enforces the
	// check+insert atomically and is released at commit/rollback.
	//
	// TODO(supply): revisit this guard. It's correct but a bit implicit, and
	// hashtext can (harmlessly) over-serialize on a collision. Cleaner options to
	// weigh later: lock the servers row (SELECT ... FOR UPDATE — simpler, but
	// serializes creates per-server rather than per-owner), or a small shared
	// idempotency/coalescing helper for the double-click case (which would also
	// prevent duplicate forum posts, though NOT the limit race on distinct
	// requests). Note bases.Register has the same race today and does not guard it.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		in.ServerID.String(), in.OwnerUserID); err != nil {
		return uuid.Nil, err
	}
	var open int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM supply_requests WHERE servers_id = $1 AND owner_user_id = $2 AND status = 'open'`,
		in.ServerID, in.OwnerUserID).Scan(&open); err != nil {
		return uuid.Nil, err
	}
	if open >= in.OpenLimit {
		return uuid.Nil, ErrLimit
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO supply_requests
			(id, servers_id, owner_user_id, thread_id, title, description, status, post_version, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, $4, $5, 'open', $6, $7, $7)`,
		id, in.ServerID, in.OwnerUserID, in.Title, in.Description, CurrentPostVersion, now); err != nil {
		return uuid.Nil, err
	}
	if err := r.enq.Enqueue(ctx, tx, outbox.Request{
		Kind:           taskCreate,
		Payload:        taskPayload{RequestID: id, AppID: in.AppID, Token: in.Token},
		ChronometricID: id,
	}); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// --- loads -------------------------------------------------------------------

func (r *pgRepository) Progress(ctx context.Context, serverID uuid.UUID, threadID string) (Progress, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx,
		`SELECT id FROM supply_requests WHERE servers_id = $1 AND thread_id = $2`, serverID, threadID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	if err != nil {
		return Progress{}, err
	}
	return r.loadByID(ctx, id)
}

func (r *pgRepository) ProgressByID(ctx context.Context, requestID uuid.UUID) (Progress, error) {
	return r.loadByID(ctx, requestID)
}

func (r *pgRepository) ProgressByIDOwned(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID) (Progress, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx,
		`SELECT id FROM supply_requests WHERE id = $1 AND servers_id = $2 AND owner_user_id = $3`,
		requestID, serverID, ownerUserID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	if err != nil {
		return Progress{}, err
	}
	return r.loadByID(ctx, id)
}

func (r *pgRepository) ProgressByItemOwned(ctx context.Context, serverID uuid.UUID, ownerUserID string, itemID uuid.UUID) (Progress, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT req.id FROM supply_request_items i
		JOIN supply_requests req ON req.id = i.supply_requests_id
		WHERE i.id = $1 AND req.servers_id = $2 AND req.owner_user_id = $3`,
		itemID, serverID, ownerUserID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	if err != nil {
		return Progress{}, err
	}
	return r.loadByID(ctx, id)
}

// --- thread lifecycle --------------------------------------------------------

func (r *pgRepository) SetThreadID(ctx context.Context, requestID uuid.UUID, threadID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE supply_requests SET thread_id = $1, post_version = $2 WHERE id = $3`,
		threadID, CurrentPostVersion, requestID)
	return err
}

// RecreatePost clears a request's thread id and re-enqueues a create task in one
// transaction — used when the live post was deleted out from under us.
func (r *pgRepository) RecreatePost(ctx context.Context, requestID uuid.UUID) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `UPDATE supply_requests SET thread_id = NULL, updated_at = $1 WHERE id = $2`, now, requestID); err != nil {
		return err
	}
	if err := r.enq.Enqueue(ctx, tx, outbox.Request{Kind: taskCreate, Payload: taskPayload{RequestID: requestID}, ChronometricID: requestID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Republish re-posts a request: with no live thread it enqueues a create; with a
// live thread it enqueues a refresh. Owner-scoped and open-only.
func (r *pgRepository) Republish(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var thread *string
	var status string
	err = tx.QueryRow(ctx,
		`SELECT thread_id, status FROM supply_requests WHERE id = $1 AND servers_id = $2 AND owner_user_id = $3 FOR UPDATE`,
		requestID, serverID, ownerUserID).Scan(&thread, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if Status(status) != StatusOpen {
		return ErrClosed
	}
	if err := touch(ctx, tx, requestID, now); err != nil {
		return err
	}
	if thread == nil || *thread == "" {
		if err := r.enq.Enqueue(ctx, tx, outbox.Request{Kind: taskCreate, Payload: taskPayload{RequestID: requestID}, ChronometricID: requestID}); err != nil {
			return err
		}
	} else if err := r.enqueueRefresh(ctx, tx, requestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- owner-scoped detail mutations -------------------------------------------

func (r *pgRepository) UpdateDetails(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID, title, description string) error {
	return r.mutateOpen(ctx, serverID, ownerUserID, requestID, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		_, err := tx.Exec(ctx,
			`UPDATE supply_requests SET title = $1, description = $2, updated_at = $3 WHERE id = $4`,
			title, description, now, requestID)
		return err
	})
}

func (r *pgRepository) SetDeliveryLocation(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID, gdid, gdVersion string) error {
	return r.mutateOpen(ctx, serverID, ownerUserID, requestID, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		_, err := tx.Exec(ctx,
			`UPDATE supply_requests SET delivery_location_gdid = $1, delivery_location_gd_version = $2, updated_at = $3 WHERE id = $4`,
			nullIfEmpty(gdid), nullIfEmpty(gdVersion), now, requestID)
		return err
	})
}

func (r *pgRepository) SetSystemInfo(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID, systemName, systemCode string, planet *int) error {
	return r.mutateOpen(ctx, serverID, ownerUserID, requestID, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		_, err := tx.Exec(ctx,
			`UPDATE supply_requests SET system_name = $1, system_code = $2, planet_number = $3, updated_at = $4 WHERE id = $5`,
			nullIfEmpty(systemName), nullIfEmpty(systemCode), planet, now, requestID)
		return err
	})
}

func (r *pgRepository) SetMessageRef(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID, guildID, channelID, messageID string) error {
	return r.mutateOpen(ctx, serverID, ownerUserID, requestID, func(ctx context.Context, tx pgx.Tx, now time.Time) error {
		_, err := tx.Exec(ctx,
			`UPDATE supply_requests SET ref_message_guild_id = $1, ref_message_channel_id = $2, ref_message_id = $3, updated_at = $4 WHERE id = $5`,
			nullIfEmpty(guildID), nullIfEmpty(channelID), nullIfEmpty(messageID), now, requestID)
		return err
	})
}

// mutateOpen runs fn inside a tx after locking the owner's open request, then
// touches it and enqueues a refresh — the shared shape of every owner-scoped
// detail edit.
func (r *pgRepository) mutateOpen(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID, fn func(ctx context.Context, tx pgx.Tx, now time.Time) error) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := lockOpenByOwner(ctx, tx, serverID, ownerUserID, requestID); err != nil {
		return err
	}
	if err := fn(ctx, tx, now); err != nil {
		return err
	}
	if err := r.enqueueRefresh(ctx, tx, requestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- items -------------------------------------------------------------------

func (r *pgRepository) AddItem(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID, gdid, gdVersion string, qty, maxItems int) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := lockOpenByOwner(ctx, tx, serverID, ownerUserID, requestID); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM supply_request_items WHERE supply_requests_id = $1`, requestID).Scan(&count); err != nil {
		return err
	}
	if count >= maxItems {
		return ErrMaxItems
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM supply_request_items WHERE supply_requests_id = $1 AND item_gdid = $2)`,
		requestID, gdid).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrItemExists
	}
	itemID, err := uuidv7.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO supply_request_items (id, supply_requests_id, item_gdid, gamedata_version, required_qty, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		itemID, requestID, gdid, gdVersion, qty, now); err != nil {
		return err
	}
	if err := touch(ctx, tx, requestID, now); err != nil {
		return err
	}
	if err := r.enqueueRefresh(ctx, tx, requestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) UpdateItemQty(ctx context.Context, serverID uuid.UUID, ownerUserID string, itemID uuid.UUID, qty int) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	requestID, err := lockOpenByItemOwner(ctx, tx, serverID, ownerUserID, itemID)
	if err != nil {
		return err
	}
	// The new required qty may not drop below what members have already reserved.
	var reserved int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(reserved_qty), 0) FROM supply_reservations WHERE supply_request_items_id = $1`,
		itemID).Scan(&reserved); err != nil {
		return err
	}
	if qty < reserved {
		return ErrQtyBelowReserved
	}
	if _, err := tx.Exec(ctx,
		`UPDATE supply_request_items SET required_qty = $1, updated_at = $2 WHERE id = $3`,
		qty, now, itemID); err != nil {
		return err
	}
	if err := touch(ctx, tx, requestID, now); err != nil {
		return err
	}
	if err := r.enqueueRefresh(ctx, tx, requestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveItem deletes an item (and its reservations) from the owner's open
// request, returning how many reservations it cleared.
func (r *pgRepository) RemoveItem(ctx context.Context, serverID uuid.UUID, ownerUserID string, itemID uuid.UUID) (int, error) {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	requestID, err := lockOpenByItemOwner(ctx, tx, serverID, ownerUserID, itemID)
	if err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM supply_reservations WHERE supply_request_items_id = $1`, itemID)
	if err != nil {
		return 0, err
	}
	cleared := int(tag.RowsAffected())
	if _, err := tx.Exec(ctx, `DELETE FROM supply_request_items WHERE id = $1`, itemID); err != nil {
		return 0, err
	}
	if err := touch(ctx, tx, requestID, now); err != nil {
		return 0, err
	}
	if err := r.enqueueRefresh(ctx, tx, requestID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return cleared, nil
}

func (r *pgRepository) Cancel(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := lockOpenByOwner(ctx, tx, serverID, ownerUserID, requestID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE supply_requests SET status = 'cancelled', closed_at = $1, updated_at = $1 WHERE id = $2`,
		now, requestID); err != nil {
		return err
	}
	if err := r.enqueueClose(ctx, tx, requestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- panel mutations (public, keyed by thread + gdid) ------------------------

func (r *pgRepository) Reserve(ctx context.Context, serverID uuid.UUID, threadID, gdid, userID string, qty int) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	requestID, err := lockOpenByThread(ctx, tx, serverID, threadID)
	if err != nil {
		return err
	}
	itemID, required, err := lockItemByGDID(ctx, tx, requestID, gdid)
	if err != nil {
		return err
	}
	var reserved int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(reserved_qty), 0) FROM supply_reservations WHERE supply_request_items_id = $1`,
		itemID).Scan(&reserved); err != nil {
		return err
	}
	if qty > required-reserved {
		return ErrOverCap
	}
	id, err := uuidv7.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO supply_reservations
			(id, supply_request_items_id, user_id, reserved_qty, delivered_qty, created_by_user_id, updated_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 0, $3, $3, $5, $5)
		ON CONFLICT (supply_request_items_id, user_id) DO UPDATE
		SET reserved_qty = supply_reservations.reserved_qty + EXCLUDED.reserved_qty,
		    updated_by_user_id = EXCLUDED.updated_by_user_id, updated_at = EXCLUDED.updated_at`,
		id, itemID, userID, qty, now); err != nil {
		return err
	}
	if err := touch(ctx, tx, requestID, now); err != nil {
		return err
	}
	if err := r.enqueueRefresh(ctx, tx, requestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) Deliver(ctx context.Context, serverID uuid.UUID, threadID, gdid, userID string, qty int) (bool, error) {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	requestID, err := lockOpenByThread(ctx, tx, serverID, threadID)
	if err != nil {
		return false, err
	}
	itemID, _, err := lockItemByGDID(ctx, tx, requestID, gdid)
	if err != nil {
		return false, err
	}
	resID, reserved, delivered, err := lockReservation(ctx, tx, itemID, userID)
	if err != nil {
		return false, err
	}
	if qty > reserved-delivered {
		return false, ErrOverReserved
	}
	if _, err := tx.Exec(ctx,
		`UPDATE supply_reservations SET delivered_qty = delivered_qty + $1, updated_by_user_id = $2, updated_at = $3 WHERE id = $4`,
		qty, userID, now, resID); err != nil {
		return false, err
	}
	complete, err := r.finishIfComplete(ctx, tx, requestID, now)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return complete, nil
}

func (r *pgRepository) Release(ctx context.Context, serverID uuid.UUID, threadID, gdid, userID string, qty int) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	requestID, err := lockOpenByThread(ctx, tx, serverID, threadID)
	if err != nil {
		return err
	}
	itemID, _, err := lockItemByGDID(ctx, tx, requestID, gdid)
	if err != nil {
		return err
	}
	resID, reserved, delivered, err := lockReservation(ctx, tx, itemID, userID)
	if err != nil {
		return err
	}
	if qty > reserved-delivered {
		return ErrBelowDelivered
	}
	newReserved := reserved - qty
	if newReserved == 0 && delivered == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM supply_reservations WHERE id = $1`, resID); err != nil {
			return err
		}
	} else if _, err := tx.Exec(ctx,
		`UPDATE supply_reservations SET reserved_qty = $1, updated_by_user_id = $2, updated_at = $3 WHERE id = $4`,
		newReserved, userID, now, resID); err != nil {
		return err
	}
	if err := touch(ctx, tx, requestID, now); err != nil {
		return err
	}
	if err := r.enqueueRefresh(ctx, tx, requestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// lockReservation locks a member's reservation row on an item, returning
// ErrNoReservation when absent.
func lockReservation(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, userID string) (resID uuid.UUID, reserved, delivered int, err error) {
	err = tx.QueryRow(ctx,
		`SELECT id, reserved_qty, delivered_qty FROM supply_reservations WHERE supply_request_items_id = $1 AND user_id = $2 FOR UPDATE`,
		itemID, userID).Scan(&resID, &reserved, &delivered)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, 0, 0, ErrNoReservation
	}
	return resID, reserved, delivered, err
}

// finishIfComplete flips a request to completed (and enqueues its close) when it
// has at least one item and none is short of its required quantity; otherwise it
// touches and enqueues a refresh. Reports whether it completed.
func (r *pgRepository) finishIfComplete(ctx context.Context, tx pgx.Tx, requestID uuid.UUID, now time.Time) (bool, error) {
	var itemCount, shortItems int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM supply_request_items WHERE supply_requests_id = $1`, requestID).Scan(&itemCount); err != nil {
		return false, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM supply_request_items i
		WHERE i.supply_requests_id = $1
		  AND i.required_qty > COALESCE((SELECT SUM(res.delivered_qty) FROM supply_reservations res WHERE res.supply_request_items_id = i.id), 0)`,
		requestID).Scan(&shortItems); err != nil {
		return false, err
	}
	complete := itemCount > 0 && shortItems == 0
	if complete {
		if _, err := tx.Exec(ctx,
			`UPDATE supply_requests SET status = 'completed', closed_at = $1, updated_at = $1 WHERE id = $2`,
			now, requestID); err != nil {
			return false, err
		}
		if err := r.enqueueClose(ctx, tx, requestID); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := touch(ctx, tx, requestID, now); err != nil {
		return false, err
	}
	if err := r.enqueueRefresh(ctx, tx, requestID); err != nil {
		return false, err
	}
	return false, nil
}

// MemberOutstanding lists the items in a request's thread the member has an
// outstanding reservation on (reserved − delivered > 0).
func (r *pgRepository) MemberOutstanding(ctx context.Context, serverID uuid.UUID, threadID, userID string) ([]MemberItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT i.item_gdid, i.gamedata_version, res.reserved_qty, res.delivered_qty
		FROM supply_reservations res
		JOIN supply_request_items i ON i.id = res.supply_request_items_id
		JOIN supply_requests req ON req.id = i.supply_requests_id
		WHERE req.servers_id = $1 AND req.thread_id = $2 AND res.user_id = $3
		  AND res.reserved_qty > res.delivered_qty
		ORDER BY i.created_at, i.id`, serverID, threadID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemberItem
	for rows.Next() {
		var m MemberItem
		if err := rows.Scan(&m.GDID, &m.GDVersion, &m.Reserved, &m.Delivered); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListByOwner returns a page of the owner's requests filtered to statuses, plus
// the total count for the filter.
func (r *pgRepository) ListByOwner(ctx context.Context, serverID uuid.UUID, ownerUserID string, statuses []Status, limit, offset int) ([]ListEntry, int, error) {
	strs := make([]string, len(statuses))
	for i, s := range statuses {
		strs[i] = string(s)
	}
	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM supply_requests WHERE servers_id = $1 AND owner_user_id = $2 AND status = ANY($3)`,
		serverID, ownerUserID, strs).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, title, status FROM supply_requests
		WHERE servers_id = $1 AND owner_user_id = $2 AND status = ANY($3)
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5`, serverID, ownerUserID, strs, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []ListEntry
	for rows.Next() {
		var e ListEntry
		var status string
		if err := rows.Scan(&e.ID, &e.Title, &status); err != nil {
			return nil, 0, err
		}
		e.Status = Status(status)
		out = append(out, e)
	}
	return out, total, rows.Err()
}
