// Package migrator runs goose migrations (embedded) at startup, before any
// Discord session begins serving.
package migrator

import (
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"go.uber.org/zap"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Run applies all up migrations using a database/sql handle derived from the
// pool's connection string.
func Run(pool *pgxpool.Pool, log *zap.Logger) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()

	if err := goose.Up(sqlDB, "migrations"); err != nil {
		return err
	}
	log.Info("migrations applied")
	return nil
}
