// Package outbox is a transactional-outbox queue: side effects (e.g. Discord REST
// calls) are persisted in the SAME transaction as the domain write that triggers
// them, then run asynchronously by a background worker. This guarantees the
// effect survives a crash (at-least-once delivery) and keeps slow/rate-limited
// calls off an interaction's ~3s deadline — the handler commits the row and acks
// immediately; the worker does the network call.
//
// Usage: a feature's repository calls Enqueuer.Enqueue(ctx, tx, Request{...})
// inside its mutation transaction, and contributes one or more Registration
// values (kind -> Handler) into the "outbox_handlers" fx group. Handlers MUST be
// idempotent, since a task may run more than once (lease re-claim after a crash).
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Config is this module's env config. Retention durations use Go duration syntax
// (no "d" unit), so the 3d / 10d defaults are written as hours.
type Config struct {
	// PollInterval is how often the worker scans for due tasks.
	PollInterval time.Duration `env:"OUTBOX_POLL_INTERVAL" envDefault:"3s"`
	// BatchSize caps how many tasks one tick leases.
	BatchSize int `env:"OUTBOX_BATCH_SIZE" envDefault:"20"`
	// MaxAttempts is how many times a task may fail before it is abandoned.
	MaxAttempts int `env:"OUTBOX_MAX_ATTEMPTS" envDefault:"10"`
	// CleanupInterval is how often terminal tasks past their retention are purged.
	CleanupInterval time.Duration `env:"OUTBOX_CLEANUP_INTERVAL" envDefault:"1h"`
	// DoneRetention / FailedRetention are how long completed / abandoned tasks are
	// kept before the cleanup pass deletes them (defaults 3d / 10d).
	DoneRetention   time.Duration `env:"OUTBOX_DONE_RETENTION" envDefault:"72h"`
	FailedRetention time.Duration `env:"OUTBOX_FAILED_RETENTION" envDefault:"240h"`
}

// Request is a task to enqueue. Payload is JSON-marshaled. Version stamps the
// payload schema (defaults to 1). ChronometricID, when set, is the collapsing
// key: the worker runs only the newest task per (Kind, ChronometricID) each tick
// and supersedes older ones (see Handler). Delay sets an initial next_try_at
// offset.
type Request struct {
	Kind           string
	Version        int
	Payload        any
	ChronometricID uuid.UUID
	Delay          time.Duration
}

// Enqueuer persists tasks. Enqueue runs on the caller's transaction so the task
// commits atomically with the domain write — the outbox guarantee.
type Enqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, req Request) error
}

// Task is a leased unit of work handed to a Handler. Version/Attempts let the
// handler branch on payload schema and on how many times it has already failed.
type Task struct {
	ID       uuid.UUID
	Kind     string
	Version  int
	Attempts int
	Payload  json.RawMessage
}

// Handler runs one task kind. It MUST be idempotent — a task may run more than
// once (e.g. a crash before the result is recorded, or a superseding re-run).
// Returning a Permanent error abandons the task immediately (no retry); any other
// error schedules a retry until MaxAttempts.
//
// Collapsing (ChronometricID): each enqueue is a durable row — tasks are never
// merged at enqueue. Per tick the worker runs only the newest task per (Kind,
// ChronometricID) and supersedes the older ones in that group. This is safe for
// tasks whose Payload is reconstructable from current state (the handler re-reads
// it); the newest run reflects the latest committed change, so a concurrent
// update can't be lost (it is a newer row that wins). Tasks that must each run
// (carry a unique side effect or notification) should use a distinct Kind so they
// never share a group — e.g. create/refresh/close are different kinds.
type Handler func(ctx context.Context, task Task) error

// Registration binds a Handler to a kind; features contribute these into the
// "outbox_handlers" fx group.
type Registration struct {
	Kind    string
	Handler Handler
}

// permanentError marks a failure that must not be retried (e.g. a misconfigured
// forum or a revoked permission — retrying can't help).
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent wraps err so the worker fails the task without retrying.
func Permanent(err error) error { return permanentError{err: err} }

// isPermanent reports whether err (or anything it wraps) is a Permanent error.
func isPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// marshalPayload is shared by the enqueuer; exposed for tests.
func marshalPayload(v any) ([]byte, error) { return json.Marshal(v) }
