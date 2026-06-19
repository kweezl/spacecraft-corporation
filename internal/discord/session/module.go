package session

import (
	"github.com/caarlos0/env/v11"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
	"go.uber.org/fx"
)

// Module provides the Manager and runs it via the fx lifecycle. Its OnStart
// hook runs after the migrator invoke, so the schema already exists. Core module.
func Module() fx.Option {
	return fx.Module("session",
		logger.Decorate("session"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(NewFactory),
		fx.Provide(newManager),
		fx.Invoke(register),
	)
}
