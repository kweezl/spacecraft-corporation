// Package testdb provides shared setup for database-backed tests.
//
// Each suite gets its own freshly-created, migrated database: SetupSuite issues
// CREATE DATABASE with a process-unique name, migrates it, and TearDownSuite
// DROPs it (WITH FORCE). Because suites never share a database there is no
// cross-process DDL contention — separate `go test` package binaries run fully
// in parallel, no advisory lock, no shared-table reset list to maintain. Within
// a suite, SetupTest truncates every table (discovered dynamically) so each test
// still starts clean. This needs no Docker at test time (unlike testcontainers),
// only a reachable Postgres named by TEST_DATABASE_URL — friendlier to CI that
// forbids Docker-in-Docker.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/migrator"
)

// DSN returns the admin connection string from TEST_DATABASE_URL — the database
// the helpers connect to in order to CREATE/DROP the per-suite databases. It
// fails the test (never skips) when the variable is unset: DB-backed tests are
// part of the safety net, so a missing configuration must be loud, not a silent
// green. The variable intentionally has no default; point it at any reachable
// database on the test server (the per-suite databases are created beside it):
// postgres://bot:bot@localhost:5432/spacecraft_test?sslmode=disable
func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is not set: DB-backed tests require a reachable Postgres " +
			"(per-suite databases are created beside it). " +
			"Example: postgres://bot:bot@localhost:5432/spacecraft_test?sslmode=disable")
	}
	return dsn
}

// dbSeq makes per-suite database names unique within a process; the pid keeps
// them unique across the parallel package test binaries `go test ./...` spawns.
var dbSeq atomic.Int64

func uniqueDBName() string {
	return fmt.Sprintf("testdb_%d_%d", os.Getpid(), dbSeq.Add(1))
}

// childDSN rewrites the admin DSN to point at the named per-suite database.
func childDSN(t *testing.T, admin, name string) string {
	t.Helper()
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("testdb: parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// newDatabase creates a uniquely-named database (optionally migrated) and returns
// a pool against it plus its name (for the eventual drop). The identifier is
// process-generated, so interpolating it is safe.
func newDatabase(t *testing.T, migrate bool) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	admin := DSN(t)
	name := uniqueDBName()

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("testdb: connect admin: %v", err)
	}
	_, err = conn.Exec(ctx, `CREATE DATABASE "`+name+`"`)
	_ = conn.Close(ctx)
	if err != nil {
		t.Fatalf("testdb: create database %q: %v", name, err)
	}

	pool, err := pgxpool.New(ctx, childDSN(t, admin, name))
	if err != nil {
		dropDatabase(t, name)
		t.Fatalf("testdb: new pool: %v", err)
	}
	if migrate {
		if err := migrator.Run(pool, zap.NewNop()); err != nil {
			pool.Close()
			dropDatabase(t, name)
			t.Fatalf("testdb: run migrations: %v", err)
		}
	}
	return pool, name
}

// dropDatabase removes a per-suite database. WITH (FORCE) terminates any lingering
// connections so a not-fully-closed pool can't block the drop. Best-effort: a
// failure is logged, not fatal, so it never masks the real test outcome.
func dropDatabase(t *testing.T, name string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, DSN(t))
	if err != nil {
		t.Logf("testdb: connect to drop %q: %v", name, err)
		return
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`); err != nil {
		t.Logf("testdb: drop database %q: %v", name, err)
	}
}

// truncateAll empties every application table (discovered from the catalog, so
// there is no list to maintain) while preserving the schema and the goose
// version. CASCADE follows foreign keys; RESTART IDENTITY resets any sequences.
func truncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'goose_db_version'`)
	if err != nil {
		t.Fatalf("testdb: list tables: %v", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			t.Fatalf("testdb: scan table name: %v", err)
		}
		names = append(names, `"`+n+`"`)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("testdb: iterate tables: %v", err)
	}
	if len(names) == 0 {
		return
	}
	if _, err := pool.Exec(ctx, `TRUNCATE `+strings.Join(names, ", ")+` RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("testdb: truncate: %v", err)
	}
}

// Suite is the base testify suite for database-backed tests. Embed it to get a
// fresh, migrated database in Pool for the whole suite, truncated between tests:
//
//	type repoSuite struct {
//		testdb.Suite
//		repo Repository
//	}
//	func (s *repoSuite) SetupSuite() { s.Suite.SetupSuite(); s.repo = newRepository(s.Pool) }
//	func TestRepo(t *testing.T) { suite.Run(t, new(repoSuite)) }
//
// An embedding suite that overrides a lifecycle hook must call the embedded one
// (e.g. s.Suite.SetupSuite()).
type Suite struct {
	suite.Suite
	// Pool is the connection pool for this suite's database.
	Pool *pgxpool.Pool
	name string
}

// SetupSuite creates and migrates this suite's database.
func (s *Suite) SetupSuite() { s.Pool, s.name = newDatabase(s.T(), true) }

// TearDownSuite closes the pool and drops the database.
func (s *Suite) TearDownSuite() {
	if s.Pool != nil {
		s.Pool.Close()
	}
	dropDatabase(s.T(), s.name)
}

// SetupTest empties the tables so each test starts from a clean, migrated schema.
func (s *Suite) SetupTest() { truncateAll(s.T(), s.Pool) }

// NewEmptyDB creates a fresh, un-migrated database and returns a pool against it;
// the database is dropped when the test ends. For tests that exercise the
// migrator itself (which need an empty schema).
func NewEmptyDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, name := newDatabase(t, false)
	t.Cleanup(func() {
		pool.Close()
		dropDatabase(t, name)
	})
	return pool
}

// SeedServer inserts an approved servers row for a Discord snowflake so child
// tables (which reference servers.id) have a parent to point at, and returns that
// servers.id — the UUID child repositories key on. Idempotent: a repeated call
// returns the existing row's id.
func SeedServer(t *testing.T, pool *pgxpool.Pool, serverID string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	// DO UPDATE (a no-op touch of server_id) so RETURNING yields the id on both
	// insert and conflict; DO NOTHING would return no row on conflict.
	err := pool.QueryRow(context.Background(),
		`INSERT INTO servers (id, server_id, name, approved, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1, '', true, now(), now())
		 ON CONFLICT (server_id) DO UPDATE SET server_id = EXCLUDED.server_id
		 RETURNING id`, serverID).Scan(&id)
	if err != nil {
		t.Fatalf("testdb: seed server %q: %v", serverID, err)
	}
	return id
}
