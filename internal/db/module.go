package db

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"
)

// Module provides *pgxpool.Pool. Core module.
func Module() fx.Option {
	return fx.Module("db",
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(New),
	)
}
