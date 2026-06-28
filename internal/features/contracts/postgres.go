package contracts

import (
	"context"
	"errors"
	"fmt"
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
	// COALESCE so a NULL (deadline-less) contract is never seen as expired: the
	// bare "deadline <= now" yields NULL for a NULL deadline, which would fail the
	// bool scan.
	err := tx.QueryRow(ctx,
		`SELECT id, status, COALESCE(deadline <= $3, false) FROM contracts
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

// lockOpenContractByID is lockOpenContract keyed by contract id + server (the
// console path). Same open-and-not-expired guard; returns the id back for
// symmetry with the thread-keyed variant.
func lockOpenContractByID(ctx context.Context, tx pgx.Tx, serverID, contractID uuid.UUID, now time.Time) (uuid.UUID, error) {
	var (
		status  string
		expired bool
	)
	err := tx.QueryRow(ctx,
		`SELECT status, COALESCE(deadline <= $3, false) FROM contracts
		 WHERE id = $1 AND servers_id = $2 FOR UPDATE`,
		contractID, serverID, now).Scan(&status, &expired)
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
	return contractID, nil
}

// lockOpenContractByItem resolves the open contract owning itemID (scoped to the
// server), locks the contract row, and enforces the open-and-not-expired guard.
// A forged/cross-server item id yields ErrNotFound. Returns the contract id.
func lockOpenContractByItem(ctx context.Context, tx pgx.Tx, serverID, itemID uuid.UUID, now time.Time) (uuid.UUID, error) {
	var (
		cid     uuid.UUID
		status  string
		expired bool
	)
	err := tx.QueryRow(ctx,
		`SELECT c.id, c.status, COALESCE(c.deadline <= $3, false)
		 FROM contract_items ci JOIN contracts c ON c.id = ci.contracts_id
		 WHERE ci.id = $1 AND c.servers_id = $2 FOR UPDATE OF c`,
		itemID, serverID, now).Scan(&cid, &status, &expired)
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
	return cid, nil
}

// asLocal reinterprets a TIMESTAMP value (which pgx decodes UTC-labeled, carrying
// the stored wall-clock numbers) as the configured local zone, so durations
// against time.Now() come out right. See the lockOpenContract note.
func asLocal(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
}

// asLocalPtr is asLocal for a nullable timestamp: nil passes through (a
// deadline-less contract), otherwise the wall clock is reinterpreted as local.
func asLocalPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := asLocal(*t)
	return &v
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

// touch advances a contract's updated_at/updated_by within a transaction. It also
// advances last_refreshed_at: every caller enqueues a refresh in the same tx, so
// the watermark must move with it, keeping the embed's "last updated" footer in
// sync with the edit that's about to happen.
func touch(ctx context.Context, tx pgx.Tx, contractID uuid.UUID, now time.Time, actor string) error {
	_, err := tx.Exec(ctx,
		`UPDATE contracts SET updated_at = $1, updated_by_user_id = $2, last_refreshed_at = $1 WHERE id = $3`,
		now, actor, contractID)
	return err
}

// enqueueNotify enqueues the pre-expiry "closing soon" notice on the caller's tx.
func (r *pgRepository) enqueueNotify(ctx context.Context, tx pgx.Tx, contractID uuid.UUID) error {
	return r.enq.Enqueue(ctx, tx, outbox.Request{
		Kind: taskNotify, Payload: taskPayload{ContractID: contractID}, ChronometricID: contractID,
	})
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

	// thread_id is NULL until the worker creates the forum thread; post_version is
	// the format the worker will post in (stamped again by SetThreadID).
	if _, err := tx.Exec(ctx, `
		INSERT INTO contracts
			(id, servers_id, thread_id, title, description, status, kind, post_version, deadline,
			 created_by_user_id, updated_by_user_id, created_at, updated_at, last_refreshed_at)
		VALUES ($1, $2, NULL, $3, $4, 'open', $5, $6, $7, $8, $8, $9, $9, $9)`,
		id, in.ServerID, in.Title, in.Description, string(in.Kind), CurrentPostVersion, in.Deadline, in.CreatedByUserID, now); err != nil {
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

// AddItemByID adds a required item to an open contract resolved by id (console).
func (r *pgRepository) AddItemByID(ctx context.Context, serverID, contractID uuid.UUID, itemName string, qty, maxItems int, actor string) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContractByID(ctx, tx, serverID, contractID, now)
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

// RemoveItemByID deletes an item (resolved by id) and cascades its reservations.
func (r *pgRepository) RemoveItemByID(ctx context.Context, serverID, itemID uuid.UUID, actor string) (uuid.UUID, int, error) {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContractByItem(ctx, tx, serverID, itemID, now)
	if err != nil {
		return uuid.Nil, 0, err
	}

	// Cascade the reservations explicitly (FK is RESTRICT), then the item.
	tag, err := tx.Exec(ctx, `DELETE FROM contract_reservations WHERE contract_items_id = $1`, itemID)
	if err != nil {
		return uuid.Nil, 0, err
	}
	cleared := int(tag.RowsAffected())
	if _, err := tx.Exec(ctx, `DELETE FROM contract_items WHERE id = $1 AND contracts_id = $2`, itemID, cid); err != nil {
		return uuid.Nil, 0, err
	}
	if err := touch(ctx, tx, cid, now, actor); err != nil {
		return uuid.Nil, 0, err
	}
	if err := r.enqueueRefresh(ctx, tx, cid); err != nil {
		return uuid.Nil, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, 0, err
	}
	return cid, cleared, nil
}

// UpdateItem renames an item (resolved by id) and sets its required quantity,
// enforcing case-insensitive name uniqueness within the contract and refusing a
// quantity below what members have already reserved on the item.
func (r *pgRepository) UpdateItem(ctx context.Context, serverID, itemID uuid.UUID, newName string, newQty int, actor string) (uuid.UUID, error) {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContractByItem(ctx, tx, serverID, itemID, now)
	if err != nil {
		return uuid.Nil, err
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM contract_items WHERE contracts_id = $1 AND lower(item_name) = lower($2) AND id <> $3)`,
		cid, newName, itemID).Scan(&exists); err != nil {
		return uuid.Nil, err
	}
	if exists {
		return uuid.Nil, ErrItemExists
	}
	var reserved int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(reserved_qty), 0) FROM contract_reservations WHERE contract_items_id = $1`,
		itemID).Scan(&reserved); err != nil {
		return uuid.Nil, err
	}
	if newQty < reserved {
		return uuid.Nil, ErrQtyBelowReserved
	}
	if _, err := tx.Exec(ctx,
		`UPDATE contract_items SET item_name = $1, required_qty = $2, updated_by_user_id = $3, updated_at = $4 WHERE id = $5 AND contracts_id = $6`,
		newName, newQty, actor, now, itemID, cid); err != nil {
		return uuid.Nil, err
	}
	if err := touch(ctx, tx, cid, now, actor); err != nil {
		return uuid.Nil, err
	}
	if err := r.enqueueRefresh(ctx, tx, cid); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return cid, nil
}

// UpdateDetails edits an open contract's title and description (console).
func (r *pgRepository) UpdateDetails(ctx context.Context, serverID, contractID uuid.UUID, title, description, actor string) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContractByID(ctx, tx, serverID, contractID, now)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE contracts SET title = $1, description = $2, updated_at = $3, updated_by_user_id = $4, last_refreshed_at = $3 WHERE id = $5`,
		title, description, now, actor, cid); err != nil {
		return err
	}
	if err := r.enqueueRefresh(ctx, tx, cid); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetDeadline sets (or clears, with nil) an open contract's deadline. It resets
// expiry_notified_at so the closing-soon notice re-arms for the new deadline.
func (r *pgRepository) SetDeadline(ctx context.Context, serverID, contractID uuid.UUID, deadline *time.Time, actor string) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContractByID(ctx, tx, serverID, contractID, now)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE contracts SET deadline = $1, expiry_notified_at = NULL, updated_at = $2, updated_by_user_id = $3, last_refreshed_at = $2 WHERE id = $4`,
		deadline, now, actor, cid); err != nil {
		return err
	}
	if err := r.enqueueRefresh(ctx, tx, cid); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

	complete, err := r.finishIfComplete(ctx, tx, cid, now, userID)
	if err != nil {
		return false, err
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

// DeliverByItem records qty delivered by a participant on an item (resolved by
// id), bounded by their outstanding (reserved−delivered), and flips the contract
// to completed in the same transaction when every item is fully delivered.
func (r *pgRepository) DeliverByItem(ctx context.Context, serverID, itemID uuid.UUID, targetUserID string, qty int, actor string) (uuid.UUID, bool, error) {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContractByItem(ctx, tx, serverID, itemID, now)
	if err != nil {
		return uuid.Nil, false, err
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
		return uuid.Nil, false, ErrNoReservation
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if qty > reserved-delivered {
		return uuid.Nil, false, ErrOverReserved
	}
	if _, err := tx.Exec(ctx,
		`UPDATE contract_reservations SET delivered_qty = delivered_qty + $1, updated_by_user_id = $2, updated_at = $3 WHERE id = $4`,
		qty, actor, now, resID); err != nil {
		return uuid.Nil, false, err
	}

	complete, err := r.finishIfComplete(ctx, tx, cid, now, actor)
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return cid, complete, nil
}

// finishIfComplete flips a contract to completed (and enqueues its close) when it
// has at least one item and none is short of its required quantity; otherwise it
// touches the contract and enqueues a refresh. Reports whether it completed.
func (r *pgRepository) finishIfComplete(ctx context.Context, tx pgx.Tx, cid uuid.UUID, now time.Time, actor string) (bool, error) {
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
			`UPDATE contracts SET status = 'completed', closed_at = $1, updated_at = $1, last_refreshed_at = $1, updated_by_user_id = $2 WHERE id = $3`,
			now, actor, cid); err != nil {
			return false, err
		}
		return true, r.enqueueClose(ctx, tx, cid)
	}
	if err := touch(ctx, tx, cid, now, actor); err != nil {
		return false, err
	}
	return false, r.enqueueRefresh(ctx, tx, cid)
}

// SetReservationByItem sets a participant's reservation on an item to an absolute
// quantity (officer): floored at what they have already delivered and capped at
// the item's remaining capacity. A value leaving the row 0/0 deletes it.
func (r *pgRepository) SetReservationByItem(ctx context.Context, serverID, itemID uuid.UUID, targetUserID string, newReserved int, actor string) (uuid.UUID, error) {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContractByItem(ctx, tx, serverID, itemID, now)
	if err != nil {
		return uuid.Nil, err
	}
	var required int
	if err := tx.QueryRow(ctx, `SELECT required_qty FROM contract_items WHERE id = $1`, itemID).Scan(&required); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrItemNotFound
		}
		return uuid.Nil, err
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
		return uuid.Nil, ErrNoReservation
	}
	if err != nil {
		return uuid.Nil, err
	}
	if newReserved < delivered {
		return uuid.Nil, ErrBelowDelivered
	}
	// Cap at the item's remaining capacity: the others' reservations plus this new
	// value may not exceed the required quantity.
	var othersReserved int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(reserved_qty), 0) FROM contract_reservations WHERE contract_items_id = $1 AND user_id <> $2`,
		itemID, targetUserID).Scan(&othersReserved); err != nil {
		return uuid.Nil, err
	}
	if newReserved > required-othersReserved {
		return uuid.Nil, ErrOverCap
	}

	if newReserved == 0 && delivered == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM contract_reservations WHERE id = $1`, resID); err != nil {
			return uuid.Nil, err
		}
	} else if _, err := tx.Exec(ctx,
		`UPDATE contract_reservations SET reserved_qty = $1, updated_by_user_id = $2, updated_at = $3 WHERE id = $4`,
		newReserved, actor, now, resID); err != nil {
		return uuid.Nil, err
	}
	if err := touch(ctx, tx, cid, now, actor); err != nil {
		return uuid.Nil, err
	}
	if err := r.enqueueRefresh(ctx, tx, cid); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return cid, nil
}

// RemoveReservation hard-deletes a participant's entire reservation on an item
// (reserved + delivered both gone). Returns the parent contract id (console).
func (r *pgRepository) RemoveReservation(ctx context.Context, serverID, itemID uuid.UUID, targetUserID, actor string) (uuid.UUID, error) {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cid, err := lockOpenContractByItem(ctx, tx, serverID, itemID, now)
	if err != nil {
		return uuid.Nil, err
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM contract_reservations WHERE contract_items_id = $1 AND user_id = $2`, itemID, targetUserID)
	if err != nil {
		return uuid.Nil, err
	}
	if tag.RowsAffected() == 0 {
		return uuid.Nil, ErrNoReservation
	}
	if err := touch(ctx, tx, cid, now, actor); err != nil {
		return uuid.Nil, err
	}
	if err := r.enqueueRefresh(ctx, tx, cid); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return cid, nil
}

// CancelByID flips an open contract (resolved by id) to cancelled (console).
func (r *pgRepository) CancelByID(ctx context.Context, serverID, contractID uuid.UUID, actor string) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM contracts WHERE id = $1 AND servers_id = $2 FOR UPDATE`,
		contractID, serverID).Scan(&status)
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
		`UPDATE contracts SET status = 'cancelled', closed_at = $1, updated_at = $1, last_refreshed_at = $1, updated_by_user_id = $2 WHERE id = $3`,
		now, actor, contractID); err != nil {
		return err
	}
	if err := r.enqueueClose(ctx, tx, contractID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// contractCols is the shared contract projection. thread_id is COALESCEd because
// it is NULL until the worker creates the thread; deadline stays nullable (a
// deadline-less contract). contractColsC is the same list qualified with the "c"
// alias for queries that join.
const contractCols = `id, servers_id, COALESCE(thread_id, ''), title, description, status, kind, post_version, deadline, created_by_user_id, last_refreshed_at`
const contractColsC = `c.id, c.servers_id, COALESCE(c.thread_id, ''), c.title, c.description, c.status, c.kind, c.post_version, c.deadline, c.created_by_user_id, c.last_refreshed_at`

func scanContract(row pgx.Row) (Progress, error) {
	var p Progress
	var status, kind string
	var deadline *time.Time
	err := row.Scan(&p.ID, &p.ServerID, &p.ThreadID, &p.Title, &p.Description, &status, &kind, &p.PostVersion, &deadline, &p.CreatedByUserID, &p.LastRefreshedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	if err != nil {
		return Progress{}, err
	}
	p.Status = Status(status)
	p.Kind = Kind(kind)
	p.Deadline = asLocalPtr(deadline)
	p.LastRefreshedAt = asLocal(p.LastRefreshedAt)
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

func (r *pgRepository) ProgressByIDScoped(ctx context.Context, serverID, contractID uuid.UUID) (Progress, error) {
	p, err := scanContract(r.pool.QueryRow(ctx,
		`SELECT `+contractCols+` FROM contracts WHERE id = $1 AND servers_id = $2`, contractID, serverID))
	if err != nil {
		return Progress{}, err
	}
	if p.Items, err = r.loadItems(ctx, p.ID); err != nil {
		return Progress{}, err
	}
	return p, nil
}

func (r *pgRepository) ProgressByItemScoped(ctx context.Context, serverID, itemID uuid.UUID) (Progress, error) {
	p, err := scanContract(r.pool.QueryRow(ctx,
		`SELECT `+contractColsC+` FROM contract_items ci JOIN contracts c ON c.id = ci.contracts_id
		 WHERE ci.id = $1 AND c.servers_id = $2`, itemID, serverID))
	if err != nil {
		return Progress{}, err
	}
	if p.Items, err = r.loadItems(ctx, p.ID); err != nil {
		return Progress{}, err
	}
	return p, nil
}

// SetThreadID records the forum thread the worker created and stamps the post's
// format version to the current one — so recreating a stale-format post (which
// clears the thread, then re-creates) lands at CurrentPostVersion and won't be
// re-migrated.
func (r *pgRepository) SetThreadID(ctx context.Context, contractID uuid.UUID, threadID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE contracts SET thread_id = $1, post_version = $2, updated_at = $3 WHERE id = $4`,
		threadID, CurrentPostVersion, time.Now(), contractID)
	return err
}

// RecreatePost clears a contract's thread id AND enqueues a fresh create-thread
// task in a single transaction — recovering a deleted post or migrating a
// stale-format one. Doing both atomically is the safety property: a crash can't
// leave thread_id cleared with no queued create (orphaning the contract with no
// post). The worker's empty-thread guard then re-posts. No interaction token
// travels with the create.
func (r *pgRepository) RecreatePost(ctx context.Context, contractID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE contracts SET thread_id = NULL, updated_at = $1 WHERE id = $2`, time.Now(), contractID); err != nil {
		return err
	}
	if err := r.enq.Enqueue(ctx, tx, outbox.Request{
		Kind: taskCreateThread, Payload: taskPayload{ContractID: contractID}, ChronometricID: contractID,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Republish enqueues the right repair task for a contract resolved by id+server:
// create the post if there is no thread, otherwise refresh it (open) or re-write
// the final embed (terminal). Reports which action it took.
func (r *pgRepository) Republish(ctx context.Context, serverID, contractID uuid.UUID) (RepublishAction, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		threadID *string
		status   string
	)
	err = tx.QueryRow(ctx,
		`SELECT thread_id, status FROM contracts WHERE id = $1 AND servers_id = $2 FOR UPDATE`,
		contractID, serverID).Scan(&threadID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}

	var action RepublishAction
	switch {
	case threadID == nil || *threadID == "":
		if err := r.enq.Enqueue(ctx, tx, outbox.Request{
			Kind: taskCreateThread, Payload: taskPayload{ContractID: contractID}, ChronometricID: contractID,
		}); err != nil {
			return "", err
		}
		action = RepublishCreating
	case Status(status) == StatusOpen:
		if err := r.enqueueRefresh(ctx, tx, contractID); err != nil {
			return "", err
		}
		action = RepublishRefreshing
	default:
		if err := r.enqueueClose(ctx, tx, contractID); err != nil {
			return "", err
		}
		action = RepublishRefreshing
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return action, nil
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

func (r *pgRepository) List(ctx context.Context, serverID uuid.UUID, statuses []Status, limit, offset int) ([]ListEntry, int, error) {
	// Empty filter defaults to open. Otherwise match any of the requested statuses
	// (status = ANY($2)).
	args := []any{serverID}
	var statusCond string
	if len(statuses) == 0 {
		args = append(args, string(StatusOpen))
		statusCond = fmt.Sprintf("c.status = $%d", len(args))
	} else {
		strs := make([]string, len(statuses))
		for i, s := range statuses {
			strs[i] = string(s)
		}
		args = append(args, strs)
		statusCond = fmt.Sprintf("c.status = ANY($%d)", len(args))
	}
	where := "c.servers_id = $1 AND " + statusCond

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
		       COALESCE((SELECT SUM(r.reserved_qty) FROM contract_reservations r
		                 JOIN contract_items ci ON ci.id = r.contract_items_id
		                 WHERE ci.contracts_id = c.id), 0),
		       COALESCE((SELECT SUM(r.delivered_qty) FROM contract_reservations r
		                 JOIN contract_items ci ON ci.id = r.contract_items_id
		                 WHERE ci.contracts_id = c.id), 0),
		       (SELECT count(DISTINCT r.user_id) FROM contract_reservations r
		        JOIN contract_items ci ON ci.id = r.contract_items_id
		        WHERE ci.contracts_id = c.id)
		FROM contracts c WHERE %s
		ORDER BY c.deadline NULLS LAST, c.id LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2), pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var page []ListEntry
	for rows.Next() {
		var e ListEntry
		var st string
		var deadline *time.Time
		if err := rows.Scan(&e.ID, &e.ServerID, &e.ThreadID, &e.Title, &e.Description, &st, &deadline,
			&e.CreatedByUserID, &e.ItemCount, &e.TotalRequired, &e.TotalReserved, &e.TotalDelivered, &e.ParticipantCount); err != nil {
			return nil, 0, err
		}
		e.Status = Status(st)
		e.Deadline = asLocalPtr(deadline)
		page = append(page, e)
	}
	return page, total, rows.Err()
}

func (r *pgRepository) Counts(ctx context.Context, serverID uuid.UUID) (Counts, error) {
	// One pass over the server's contracts, partitioning open rows into
	// unpublished (no forum thread yet) vs active (posted) and tallying the two
	// terminal states.
	var c Counts
	err := r.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE status = 'open' AND thread_id IS NULL),
			count(*) FILTER (WHERE status = 'open' AND thread_id IS NOT NULL),
			count(*) FILTER (WHERE status = 'completed'),
			count(*) FILTER (WHERE status = 'cancelled')
		FROM contracts WHERE servers_id = $1`, serverID).
		Scan(&c.Unpublished, &c.Active, &c.Completed, &c.Cancelled)
	if err != nil {
		return Counts{}, err
	}
	return c, nil
}

func (r *pgRepository) KindByID(ctx context.Context, serverID, contractID uuid.UUID) (Kind, error) {
	var kind string
	err := r.pool.QueryRow(ctx,
		`SELECT kind FROM contracts WHERE id = $1 AND servers_id = $2`, contractID, serverID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return Kind(kind), nil
}

func (r *pgRepository) KindByItem(ctx context.Context, serverID, itemID uuid.UUID) (Kind, error) {
	var kind string
	err := r.pool.QueryRow(ctx,
		`SELECT c.kind FROM contract_items ci JOIN contracts c ON c.id = ci.contracts_id
		 WHERE ci.id = $1 AND c.servers_id = $2`, itemID, serverID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return Kind(kind), nil
}

func (r *pgRepository) DueContracts(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id FROM contracts
		 WHERE status = 'open' AND deadline IS NOT NULL AND deadline <= $1
		 ORDER BY deadline LIMIT $2`, now, limit)
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

	// Re-check the deadline in the UPDATE: it may have been cleared or extended
	// between the DueContracts scan and here, in which case we must not expire it.
	tag, err := tx.Exec(ctx,
		`UPDATE contracts SET status = 'expired', closed_at = $1, updated_at = $1, last_refreshed_at = $1, updated_by_user_id = $2
		 WHERE id = $3 AND status = 'open' AND deadline IS NOT NULL AND deadline <= $1`, now, systemActor, id)
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

// queryIDs runs a query returning a single uuid column into a slice (shared by
// the sweeper scans).
func queryIDs(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, sql, args...)
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

func (r *pgRepository) NotifyDue(ctx context.Context, now time.Time, within time.Duration, limit int) ([]uuid.UUID, error) {
	// Only surface contracts that have someone to ping. The notice is a one-shot
	// latch, so a contract entering the window with zero participants must NOT be
	// latched yet — short-deadline contracts can legitimately gain their first
	// reservation after the window opens, and they still deserve the ping. The
	// latch is therefore deferred until a participant exists.
	return queryIDs(ctx, r.pool,
		`SELECT id FROM contracts c
		 WHERE status = 'open' AND expiry_notified_at IS NULL
		   AND deadline IS NOT NULL AND deadline > $1 AND deadline <= $2
		   AND EXISTS (
		       SELECT 1 FROM contract_reservations r
		       JOIN contract_items ci ON ci.id = r.contract_items_id
		       WHERE ci.contracts_id = c.id)
		 ORDER BY deadline LIMIT $3`, now, now.Add(within), limit)
}

func (r *pgRepository) MarkNotified(ctx context.Context, id uuid.UUID, now time.Time) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The EXISTS guard makes the latch atomic with "has a participant": if the last
	// reservation was released between the NotifyDue scan and here, this affects
	// zero rows and the contract stays un-latched for a future sweep — so the
	// one-shot is never consumed without someone to ping.
	tag, err := tx.Exec(ctx,
		`UPDATE contracts SET expiry_notified_at = $1
		 WHERE id = $2 AND status = 'open' AND expiry_notified_at IS NULL
		   AND EXISTS (
		       SELECT 1 FROM contract_reservations r
		       JOIN contract_items ci ON ci.id = r.contract_items_id
		       WHERE ci.contracts_id = contracts.id)`, now, id)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, nil // already notified, closed, or no participant to ping
	}
	if err := r.enqueueNotify(ctx, tx, id); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (r *pgRepository) OutstandingParticipantUserIDs(ctx context.Context, contractID uuid.UUID) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT r.user_id
		FROM contract_reservations r
		JOIN contract_items ci ON ci.id = r.contract_items_id
		WHERE ci.contracts_id = $1 AND r.reserved_qty > r.delivered_qty
		ORDER BY r.user_id`, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}
