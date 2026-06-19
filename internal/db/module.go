package db

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides *pgxpool.Pool. Core module.
func Module() fx.Option {
	return fx.Module("db",
		logger.Decorate("db"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(New),
	)
}
