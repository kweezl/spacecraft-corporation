package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kweezl/spacecraft-corporation/internal/instrumentation"
)

// newReadinessCheck contributes a "postgres" probe to the instrumentation
// readiness group: /readyz stays red until the pool can ping the database, and
// goes red again if the connection is later lost. Constructed lazily (only when
// the instrumentation module collects the group), so the --migrate graph, which
// has no instrumentation server, never builds it.
func newReadinessCheck(pool *pgxpool.Pool) instrumentation.ReadinessCheck {
	return instrumentation.ReadinessCheck{
		Name:  "postgres",
		Probe: func(ctx context.Context) error { return pool.Ping(ctx) },
	}
}
