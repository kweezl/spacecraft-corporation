// Package testdb provides shared setup for database-backed tests.
//
// In CI every package's test binary runs against a single shared Postgres
// instance, and each DB test wants a clean schema — so they must not drop and
// re-migrate concurrently or they collide on DDL. The helpers here serialize
// that critical section with a Postgres session advisory lock, which works
// across the separate OS processes `go test ./...` spawns. The lock is held for
// the duration of the test, so concurrent test packages simply take turns.
package testdb

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/migrator"
)

// lockKey is an arbitrary but stable key shared by all DB tests so they
// serialize against one another.
const lockKey = 4242

// appTables are dropped to reset to a clean slate. goose_db_version is included
// so migrations re-run from scratch.
const appTables = `ping_log, server_event, servers, goose_db_version`

// Clean acquires the cross-process lock and drops all application tables,
// returning a pool against an empty (un-migrated) database. Use it when the test
// itself exercises the migrator; use Reset when the test needs the schema in
// place. The lock and pool are released when the test ends.
func Clean(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	// A dedicated single connection holds the session-level advisory lock for
	// the whole test; a pool can't, since the lock is scoped to one connection.
	lockConn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("testdb: connect for lock: %v", err)
	}
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		t.Fatalf("testdb: acquire advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = lockConn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, lockKey)
		_ = lockConn.Close(ctx)
	})

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("testdb: new pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS `+appTables); err != nil {
		t.Fatalf("testdb: drop tables: %v", err)
	}
	return pool
}

// Reset returns a clean, fully-migrated database, serialized against other DB
// tests. It is Clean followed by running the migrations.
func Reset(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool := Clean(t, dsn)
	if err := migrator.Run(pool, zap.NewNop()); err != nil {
		t.Fatalf("testdb: run migrations: %v", err)
	}
	return pool
}
