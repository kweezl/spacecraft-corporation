package contracts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kweezl/spacecraft-corporation/internal/outbox"
	"github.com/kweezl/spacecraft-corporation/internal/uuidv7"
)

// systemActor is the audit actor recorded for status changes the sweeper makes
// (no human invoker).
const systemActor = "system"

type pgRepository struct {
	pool *pgxpool.Pool
	enq  outbox.Enqueuer
}

func newRepository(pool *pgxpool.Pool, enq outbox.Enqueuer) Repository {
	return &pgRepository{pool: pool, enq: enq}
}

// enqueueRefresh / enqueueClose enqueue the matching Discord side effect on the
// caller's transaction, so the effect commits atomically with the domain change
// (transactional outbox). ChronometricID = the contract id, so the worker
// collapses a rush of these per contract to the newest (per kind).
func (r *pgRepository) enqueueRefresh(ctx context.Context, tx pgx.Tx, contractID uuid.UUID) error {
	return r.enq.Enqueue(ctx, tx, outbox.Request{
		Kind: taskRefresh, Payload: taskPayload{ContractID: contractID}, ChronometricID: contractID,
	})
}

func (r *pgRepository) enqueueClose(ctx context.Context, tx pgx.Tx, contractID uuid.UUID) error {
	return r.enq.Enqueue(ctx, tx, outbox.Request{
		Kind: taskClose, Payload: taskPayload{ContractID: contractID}, ChronometricID: contractID,
	})
}

// lockOpenContract resolves the contract for a (server, thread), takes a row lock
// so concurrent mutations serialize, and enforces the open-and-not-expired guard.
// It returns ErrNotFound / ErrClosed / ErrExpired so the handler renders the
// right message. now is the lazy-deadline reference (see ErrExpired).
func lockOpenContract(ctx context.Context, tx pgx.Tx, serverID uuid.UUID, threadID string, now time.Time) (uuid.UUID, error) {
	var (
		id      uuid.UUID
		status  string
		expired bool
	)
	// The deadline comparison is done in SQL (deadline <= $3): the column is
	// TIMESTAMP without time zone, and pgx round-trips the wall clock symmetrically
	// for both the stored value and the bound now, so comparing in the database is
	// correct regardless of the process timezone (a Go-side compare against a
	// time.Local now would be off by the zone offset).
	err := tx.QueryRow(ctx,
		`SELECT id, status, deadline <= $3 FROM contracts
		 WHERE servers_id = $1 AND thread_id = $2 FOR UPDATE`,
		serverID, threadID, now).Scan(&id, &status, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if Status(status) != StatusOpen {
		return uuid.Nil, ErrClosed
	}
	if expired {
		return uuid.Nil, ErrExpired
	}
	return id, nil
}

// asLocal reinterprets a TIMESTAMP value (which pgx decodes UTC-labeled, carrying
// the stored wall-clock numbers) as the configured local zone, so durations
// against time.Now() come out right. See the lockOpenContract note.
func asLocal(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
}

// lockItem resolves a required item by (case-insensitive) name within a contract
// and locks it. Returns ErrItemNotFound when absent.
func lockItem(ctx context.Context, tx pgx.Tx, contractID uuid.UUID, itemName string) (id uuid.UUID, required int, err error) {
	err = tx.QueryRow(ctx,
		`SELECT id, required_qty FROM contract_items
		 WHERE contracts_id = $1 AND lower(item_name) = lower($2) FOR UPDATE`,
		contractID, itemName).Scan(&id, &required)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, 0, ErrItemNotFound
	}
	return id, required, err
}

// touch advances a contract's updated_at/updated_by within a transaction.
func touch(ctx context.Context, tx pgx.Tx, contractID uuid.UUID, now time.Time, actor string) error {
	_, err := tx.Exec(ctx,
		`UPDATE contracts SET updated_at = $1, updated_by_user_id = $2 WHERE id = $3`,
		now, actor, contractID)
	return err
}

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

	// thread_id is NULL until the worker creates the forum thread.
	if _, err := tx.Exec(ctx, `
		INSERT INTO contracts
			(id, servers_id, thread_id, title, description, status, deadline,
			 created_by_user_id, updated_by_user_id, created_at, updated_at)
		VALUES ($1, $2, NULL, $3, $4, 'open', $5, $6, $6, $7, $7)`,
		id, in.ServerID, in.Title, in.Description, in.Deadline, in.CreatedByUserID, now); err != nil {
		return uuid.Nil, err
	}
	// Same transaction: enqueue the thread creation so it can't be lost and runs
	// off the interaction deadline.
	if err := r.enq.Enqueue(ctx, tx, outbox.Request{
		Kind:           taskCreateThread,
		Payload:        taskPayload{ContractID: id, AppID: in.AppID, Token: in.Token},
		ChronometricID: id,
	}); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (r *pgRepository) AddItem(ctx context.Context, serverID uuid.UUID, threadID, itemName string, qty, maxItems int, actor string) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContract(ctx, tx, serverID, threadID, now)
	if err != nil {
		return err
	}

	var count int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM contract_items WHERE contracts_id = $1`, cid).Scan(&count); err != nil {
		return err
	}
	if count >= maxItems {
		return ErrMaxItems
	}

	// The contract row is locked, so concurrent add-item txs serialize and this
	// existence check is race-free (cheaper than decoding a unique-violation code).
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM contract_items WHERE contracts_id = $1 AND lower(item_name) = lower($2))`,
		cid, itemName).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrItemExists
	}

	id, err := uuidv7.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO contract_items
			(id, contracts_id, item_name, required_qty, created_by_user_id, updated_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5, $6, $6)`,
		id, cid, itemName, qty, actor, now); err != nil {
		return err
	}
	if err := touch(ctx, tx, cid, now, actor); err != nil {
		return err
	}
	if err := r.enqueueRefresh(ctx, tx, cid); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) RemoveItem(ctx context.Context, serverID uuid.UUID, threadID, itemName, actor string) (int, error) {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContract(ctx, tx, serverID, threadID, now)
	if err != nil {
		return 0, err
	}
	itemID, _, err := lockItem(ctx, tx, cid, itemName)
	if err != nil {
		return 0, err
	}

	// Cascade the reservations explicitly (FK is RESTRICT), then the item.
	tag, err := tx.Exec(ctx, `DELETE FROM contract_reservations WHERE contract_items_id = $1`, itemID)
	if err != nil {
		return 0, err
	}
	cleared := int(tag.RowsAffected())
	if _, err := tx.Exec(ctx, `DELETE FROM contract_items WHERE id = $1`, itemID); err != nil {
		return 0, err
	}
	if err := touch(ctx, tx, cid, now, actor); err != nil {
		return 0, err
	}
	if err := r.enqueueRefresh(ctx, tx, cid); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return cleared, nil
}

func (r *pgRepository) Participate(ctx context.Context, serverID uuid.UUID, threadID, itemName, userID string, qty int) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContract(ctx, tx, serverID, threadID, now)
	if err != nil {
		return err
	}
	itemID, required, err := lockItem(ctx, tx, cid, itemName)
	if err != nil {
		return err
	}

	var reserved int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(reserved_qty), 0) FROM contract_reservations WHERE contract_items_id = $1`,
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
		INSERT INTO contract_reservations
			(id, contract_items_id, user_id, reserved_qty, delivered_qty,
			 created_by_user_id, updated_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 0, $3, $3, $5, $5)
		ON CONFLICT (contract_items_id, user_id) DO UPDATE
		SET reserved_qty = contract_reservations.reserved_qty + EXCLUDED.reserved_qty,
		    updated_by_user_id = EXCLUDED.updated_by_user_id,
		    updated_at = EXCLUDED.updated_at`,
		id, itemID, userID, qty, now); err != nil {
		return err
	}
	if err := touch(ctx, tx, cid, now, userID); err != nil {
		return err
	}
	if err := r.enqueueRefresh(ctx, tx, cid); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) Deliver(ctx context.Context, serverID uuid.UUID, threadID, itemName, userID string, qty int) (bool, error) {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContract(ctx, tx, serverID, threadID, now)
	if err != nil {
		return false, err
	}
	itemID, _, err := lockItem(ctx, tx, cid, itemName)
	if err != nil {
		return false, err
	}

	var (
		resID               uuid.UUID
		reserved, delivered int
	)
	err = tx.QueryRow(ctx,
		`SELECT id, reserved_qty, delivered_qty FROM contract_reservations
		 WHERE contract_items_id = $1 AND user_id = $2 FOR UPDATE`,
		itemID, userID).Scan(&resID, &reserved, &delivered)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNoReservation
	}
	if err != nil {
		return false, err
	}
	if qty > reserved-delivered {
		return false, ErrOverReserved
	}

	if _, err := tx.Exec(ctx,
		`UPDATE contract_reservations SET delivered_qty = delivered_qty + $1, updated_by_user_id = $2, updated_at = $3 WHERE id = $4`,
		qty, userID, now, resID); err != nil {
		return false, err
	}

	// Complete when the contract has at least one item and none is short of its
	// required quantity (summed over all members' deliveries).
	var itemCount, shortItems int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM contract_items WHERE contracts_id = $1`, cid).Scan(&itemCount); err != nil {
		return false, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM contract_items ci
		WHERE ci.contracts_id = $1
		  AND ci.required_qty > COALESCE(
		      (SELECT SUM(r.delivered_qty) FROM contract_reservations r WHERE r.contract_items_id = ci.id), 0)`,
		cid).Scan(&shortItems); err != nil {
		return false, err
	}
	complete := itemCount > 0 && shortItems == 0

	if complete {
		if _, err := tx.Exec(ctx,
			`UPDATE contracts SET status = 'completed', closed_at = $1, updated_at = $1, updated_by_user_id = $2 WHERE id = $3`,
			now, userID, cid); err != nil {
			return false, err
		}
		if err := r.enqueueClose(ctx, tx, cid); err != nil {
			return false, err
		}
	} else {
		if err := touch(ctx, tx, cid, now, userID); err != nil {
			return false, err
		}
		if err := r.enqueueRefresh(ctx, tx, cid); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return complete, nil
}

func (r *pgRepository) Release(ctx context.Context, serverID uuid.UUID, threadID, itemName, targetUserID string, qty int, actor string) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContract(ctx, tx, serverID, threadID, now)
	if err != nil {
		return err
	}
	itemID, _, err := lockItem(ctx, tx, cid, itemName)
	if err != nil {
		return err
	}

	var (
		resID               uuid.UUID
		reserved, delivered int
	)
	err = tx.QueryRow(ctx,
		`SELECT id, reserved_qty, delivered_qty FROM contract_reservations
		 WHERE contract_items_id = $1 AND user_id = $2 FOR UPDATE`,
		itemID, targetUserID).Scan(&resID, &reserved, &delivered)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoReservation
	}
	if err != nil {
		return err
	}
	if qty > reserved-delivered {
		return ErrBelowDelivered
	}

	newReserved := reserved - qty
	if newReserved == 0 && delivered == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM contract_reservations WHERE id = $1`, resID); err != nil {
			return err
		}
	} else if _, err := tx.Exec(ctx,
		`UPDATE contract_reservations SET reserved_qty = $1, updated_by_user_id = $2, updated_at = $3 WHERE id = $4`,
		newReserved, actor, now, resID); err != nil {
		return err
	}
	if err := touch(ctx, tx, cid, now, actor); err != nil {
		return err
	}
	if err := r.enqueueRefresh(ctx, tx, cid); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) Cancel(ctx context.Context, serverID uuid.UUID, threadID, actor string) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		id     uuid.UUID
		status string
	)
	err = tx.QueryRow(ctx,
		`SELECT id, status FROM contracts WHERE servers_id = $1 AND thread_id = $2 FOR UPDATE`,
		serverID, threadID).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if Status(status) != StatusOpen {
		return ErrClosed
	}
	if _, err := tx.Exec(ctx,
		`UPDATE contracts SET status = 'cancelled', closed_at = $1, updated_at = $1, updated_by_user_id = $2 WHERE id = $3`,
		now, actor, id); err != nil {
		return err
	}
	if err := r.enqueueClose(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// contractCols is the shared contract projection. thread_id is COALESCEd because
// it is NULL until the worker creates the thread.
const contractCols = `id, servers_id, COALESCE(thread_id, ''), title, description, status, deadline, created_by_user_id`

func scanContract(row pgx.Row) (Progress, error) {
	var p Progress
	var status string
	err := row.Scan(&p.ID, &p.ServerID, &p.ThreadID, &p.Title, &p.Description, &status, &p.Deadline, &p.CreatedByUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	if err != nil {
		return Progress{}, err
	}
	p.Status = Status(status)
	p.Deadline = asLocal(p.Deadline)
	return p, nil
}

func (r *pgRepository) Progress(ctx context.Context, serverID uuid.UUID, threadID string) (Progress, error) {
	p, err := scanContract(r.pool.QueryRow(ctx,
		`SELECT `+contractCols+` FROM contracts WHERE servers_id = $1 AND thread_id = $2`, serverID, threadID))
	if err != nil {
		return Progress{}, err
	}
	if p.Items, err = r.loadItems(ctx, p.ID); err != nil {
		return Progress{}, err
	}
	return p, nil
}

func (r *pgRepository) ProgressByID(ctx context.Context, contractID uuid.UUID) (Progress, error) {
	p, err := scanContract(r.pool.QueryRow(ctx,
		`SELECT `+contractCols+` FROM contracts WHERE id = $1`, contractID))
	if err != nil {
		return Progress{}, err
	}
	if p.Items, err = r.loadItems(ctx, p.ID); err != nil {
		return Progress{}, err
	}
	return p, nil
}

func (r *pgRepository) SetThreadID(ctx context.Context, contractID uuid.UUID, threadID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE contracts SET thread_id = $1, updated_at = $2 WHERE id = $3`, threadID, time.Now(), contractID)
	return err
}

// loadItems returns a contract's items with reserved/delivered aggregates.
func (r *pgRepository) loadItems(ctx context.Context, contractID uuid.UUID) ([]Item, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ci.id, ci.item_name, ci.required_qty,
		       COALESCE(SUM(r.reserved_qty), 0), COALESCE(SUM(r.delivered_qty), 0)
		FROM contract_items ci
		LEFT JOIN contract_reservations r ON r.contract_items_id = ci.id
		WHERE ci.contracts_id = $1
		GROUP BY ci.id, ci.item_name, ci.required_qty, ci.created_at
		ORDER BY ci.created_at, ci.id`, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Name, &it.RequiredQty, &it.ReservedQty, &it.DeliveredQty); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Attach the per-member breakdown onto the (already item-ordered) items, so the
	// embed can list contributors under each item.
	parts, err := r.loadParticipants(ctx, contractID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Participants = parts[items[i].ID]
	}
	return items, nil
}

// loadParticipants returns each item's per-member reservation lines, keyed by
// contract_items_id and ordered by user within an item. Released-to-zero rows are
// already deleted, so every row here is a live contribution.
func (r *pgRepository) loadParticipants(ctx context.Context, contractID uuid.UUID) (map[uuid.UUID][]Participant, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.contract_items_id, r.user_id, r.reserved_qty, r.delivered_qty
		FROM contract_reservations r
		JOIN contract_items ci ON ci.id = r.contract_items_id
		WHERE ci.contracts_id = $1
		ORDER BY r.user_id`, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parts := make(map[uuid.UUID][]Participant)
	for rows.Next() {
		var itemID uuid.UUID
		var p Participant
		if err := rows.Scan(&itemID, &p.UserID, &p.Reserved, &p.Delivered); err != nil {
			return nil, err
		}
		parts[itemID] = append(parts[itemID], p)
	}
	return parts, rows.Err()
}

func (r *pgRepository) MemberOutstanding(ctx context.Context, serverID uuid.UUID, threadID, userID string) ([]MemberItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ci.item_name, r.reserved_qty, r.delivered_qty
		FROM contract_reservations r
		JOIN contract_items ci ON ci.id = r.contract_items_id
		JOIN contracts c ON c.id = ci.contracts_id
		WHERE c.servers_id = $1 AND c.thread_id = $2 AND r.user_id = $3
		  AND r.reserved_qty > r.delivered_qty
		ORDER BY ci.item_name`, serverID, threadID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemberItem
	for rows.Next() {
		var m MemberItem
		if err := rows.Scan(&m.Name, &m.Reserved, &m.Delivered); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *pgRepository) List(ctx context.Context, serverID uuid.UUID, status string, limit, offset int) ([]ListEntry, int, error) {
	conds := []string{"c.servers_id = $1"}
	args := []any{serverID}
	if status != "all" {
		filter := status
		if filter == "" {
			filter = string(StatusOpen)
		}
		args = append(args, filter)
		conds = append(conds, fmt.Sprintf("c.status = $%d", len(args)))
	}
	where := strings.Join(conds, " AND ")

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM contracts c WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT c.id, c.servers_id, COALESCE(c.thread_id, ''), c.title, c.description, c.status, c.deadline, c.created_by_user_id,
		       (SELECT count(*) FROM contract_items ci WHERE ci.contracts_id = c.id),
		       COALESCE((SELECT SUM(ci.required_qty) FROM contract_items ci WHERE ci.contracts_id = c.id), 0),
		       COALESCE((SELECT SUM(r.delivered_qty) FROM contract_reservations r
		                 JOIN contract_items ci ON ci.id = r.contract_items_id
		                 WHERE ci.contracts_id = c.id), 0)
		FROM contracts c WHERE %s
		ORDER BY c.deadline, c.id LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2), pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var page []ListEntry
	for rows.Next() {
		var e ListEntry
		var st string
		if err := rows.Scan(&e.ID, &e.ServerID, &e.ThreadID, &e.Title, &e.Description, &st, &e.Deadline,
			&e.CreatedByUserID, &e.ItemCount, &e.TotalRequired, &e.TotalDelivered); err != nil {
			return nil, 0, err
		}
		e.Status = Status(st)
		e.Deadline = asLocal(e.Deadline)
		page = append(page, e)
	}
	return page, total, rows.Err()
}

func (r *pgRepository) DueContracts(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id FROM contracts
		 WHERE status = 'open' AND deadline <= $1 ORDER BY deadline LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *pgRepository) MarkExpired(ctx context.Context, id uuid.UUID, now time.Time) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`UPDATE contracts SET status = 'expired', closed_at = $1, updated_at = $1, updated_by_user_id = $2
		 WHERE id = $3 AND status = 'open'`, now, systemActor, id)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, nil // already closed by another tick/instance
	}
	// Same transaction: enqueue the thread close so the expiry and its side effect
	// commit together.
	if err := r.enqueueClose(ctx, tx, id); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
