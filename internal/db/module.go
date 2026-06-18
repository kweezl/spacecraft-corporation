package db

import (
	"github.com/caarlos0/env/v11"
	"github.com/kweezl/spacecraft-cadet/internal/logger"
	"go.uber.org/fx"
)

// Module provides *pgxpool.Pool. Core module.
func Module() fx.Option {
	return fx.Module("db",
		logger.Decorate("db"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(New),
	)
}
