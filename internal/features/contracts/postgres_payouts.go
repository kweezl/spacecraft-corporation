package contracts

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kweezl/spacecraft-corporation/internal/uuidv7"
)

// The contract_payouts persistence: written once by the payout worker
// (SavePayouts), then only read — the posted report, its CSV, and the console
// reprint all render from these rows so retries never recompute.

func (r *pgRepository) Payouts(ctx context.Context, contractID uuid.UUID) ([]Payout, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id, user_name, amount, share_percent
		FROM contract_payouts WHERE contracts_id = $1 ORDER BY user_id`, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Payout
	for rows.Next() {
		var p Payout
		if err := rows.Scan(&p.UserID, &p.UserName, &p.Amount, &p.SharePercent); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *pgRepository) SavePayouts(ctx context.Context, contractID uuid.UUID, payouts []Payout, decimals int32) error {
	now := time.Now()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, p := range payouts {
		id, err := uuidv7.NewUUID()
		if err != nil {
			return err
		}
		// DO NOTHING on the (contracts_id, user_id) unique: rows a crashed earlier
		// attempt already committed win — posted figures never change.
		if _, err := tx.Exec(ctx, `
			INSERT INTO contract_payouts (id, contracts_id, user_id, user_name, amount, share_percent, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (contracts_id, user_id) DO NOTHING`,
			id, contractID, p.UserID, p.UserName, p.Amount, p.SharePercent, now); err != nil {
			return err
		}
	}
	// Freeze the compute precision on the contract, first-write-wins (IS NULL) so a
	// retry after a config change can't restamp what the committed rows were
	// computed at — republish reads this back and reproduces the figures.
	if _, err := tx.Exec(ctx,
		`UPDATE contracts SET payout_decimals = $1 WHERE id = $2 AND payout_decimals IS NULL`,
		decimals, contractID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) MarkPayoutPosted(ctx context.Context, contractID uuid.UUID, channelID, messageID string, now time.Time) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE contracts SET payout_posted_at = $1, payout_report_channel_id = $2, payout_report_message_id = $3, updated_at = $1 WHERE id = $4`,
		now, nullIfEmpty(channelID), nullIfEmpty(messageID), contractID)
	return err
}

func (r *pgRepository) RequestPayoutRepost(ctx context.Context, serverID, contractID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Resolve scoped, completed, and actually rewarded (the same predicate that
	// both shows the Reprint button and enqueued the original payout): anything
	// else is a forged or stale button, refused here instead of enqueuing a task
	// doomed to die as a permanent error.
	var cid uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM contracts
		WHERE id = $1 AND servers_id = $2 AND status = 'completed'
		  AND reward_corpo_credits > 0 AND participant_reward_factor > 0`,
		contractID, serverID).Scan(&cid)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := r.enqueuePayout(ctx, tx, cid, true); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) MarkPayoutsPaid(ctx context.Context, serverID, contractID uuid.UUID, actor string, now time.Time) (bool, error) {
	// The WHERE clause is the guard: zero rows = not this server's contract, not
	// completed, or already marked — the concurrent double-press loser.
	tag, err := r.pool.Exec(ctx, `
		UPDATE contracts SET payouts_paid_at = $1, payouts_paid_by_user_id = $2, updated_at = $1, updated_by_user_id = $2
		WHERE id = $3 AND servers_id = $4 AND status = 'completed' AND payouts_paid_at IS NULL`,
		now, actor, contractID, serverID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
