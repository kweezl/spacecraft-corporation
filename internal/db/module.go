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
		// Contribute a "postgres" readiness probe (a pool ping) to the
		// instrumentation group; lazy, so the slim --migrate graph never builds it.
		fx.Provide(fx.Annotate(
			newReadinessCheck,
			fx.ResultTags(`group:"readiness_checks"`),
		)),
	)
}
