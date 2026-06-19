// Package db provides a pgx connection pool with fx lifecycle management.
package db

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config is this module's env config.
//
// The connection string is a secret (it embeds the password). Provide it
// directly via DATABASE_URL (convenient for dev), or point DATABASE_URL_FILE at
// a mounted secret file — the ",file" option makes env read that file's
// contents. Prefer the file in prod (Docker/K8s secret): files can be 0400, live
// in tmpfs, and don't leak via `docker inspect` or /proc/<pid>/environ the way
// env vars can. URLFile wins if both are set. Mirrors BOT_TOKEN/BOT_TOKEN_FILE.
type Config struct {
	URL     string `env:"DATABASE_URL"`
	URLFile string `env:"DATABASE_URL_FILE,file"`
}

// databaseURL resolves the DSN, preferring the file-mounted secret. Whitespace
// is trimmed so a trailing newline in a secret file doesn't corrupt the DSN.
func (c Config) databaseURL() (string, error) {
	if u := strings.TrimSpace(c.URLFile); u != "" {
		return u, nil
	}
	if u := strings.TrimSpace(c.URL); u != "" {
		return u, nil
	}
	return "", errors.New("db: set DATABASE_URL or DATABASE_URL_FILE")
}

// New creates a pool and wires Ping (on start) / Close (on stop) into fx.
func New(lc fx.Lifecycle, cfg Config, log *zap.Logger) (*pgxpool.Pool, error) {
	dsn, err := cfg.databaseURL()
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return err
			}
			log.Info("database connected")
			return nil
		},
		OnStop: func(context.Context) error {
			pool.Close()
			return nil
		},
	})
	return pool, nil
}
