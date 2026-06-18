// Package db provides a pgx connection pool with fx lifecycle management.
package db

import (
	"context"

	"github.com/caarlos0/env/v11"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config is this module's env config.
type Config struct {
	DatabaseURL string `env:"DATABASE_URL,required"`
}

// New creates a pool and wires Ping (on start) / Close (on stop) into fx.
func New(lc fx.Lifecycle, cfg Config, log *zap.Logger) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
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

// Module provides *pgxpool.Pool.
var Module = fx.Module("db",
	fx.Provide(env.ParseAs[Config]),
	fx.Provide(New),
)
