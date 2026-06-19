// Package db provides a pgx connection pool with fx lifecycle management.
package db

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config is this module's env config. The connection string is assembled from
// the POSTGRES_* parts (the same set that configures the `postgres` container),
// so there is no separate DATABASE_URL to keep in sync.
//
// The password is the secret: prefer POSTGRES_PASSWORD_FILE pointing at a mounted
// secret file — the ",file" option makes env read that file's contents, which
// can be 0400, live in tmpfs, and don't leak via `docker inspect` or
// /proc/<pid>/environ the way env vars can. PasswordFile wins over Password.
// Mirrors BOT_TOKEN/BOT_TOKEN_FILE.
type Config struct {
	Host         string `env:"POSTGRES_HOST" envDefault:"localhost"`
	Port         int    `env:"POSTGRES_PORT" envDefault:"5432"`
	User         string `env:"POSTGRES_USER" envDefault:"bot"`
	Password     string `env:"POSTGRES_PASSWORD" envDefault:"bot"`
	PasswordFile string `env:"POSTGRES_PASSWORD_FILE,file"`
	DB           string `env:"POSTGRES_DB" envDefault:"spacecraft"`
	SSLMode      string `env:"POSTGRES_SSLMODE" envDefault:"disable"`
}

// DSN builds the pgx connection string from the POSTGRES_* parts. The password
// prefers the file-mounted secret over POSTGRES_PASSWORD, trimmed so a trailing
// newline in a secret file doesn't corrupt it. url.UserPassword percent-encodes
// the credentials so special characters survive.
func (c Config) DSN() string {
	pw := strings.TrimSpace(c.Password)
	if f := strings.TrimSpace(c.PasswordFile); f != "" {
		pw = f
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, pw),
		Host:     net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:     "/" + c.DB,
		RawQuery: url.Values{"sslmode": {c.SSLMode}}.Encode(),
	}
	return u.String()
}

// New creates a pool and wires Ping (on start) / Close (on stop) into fx.
func New(lc fx.Lifecycle, cfg Config, log *zap.Logger) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(context.Background(), cfg.DSN())
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
