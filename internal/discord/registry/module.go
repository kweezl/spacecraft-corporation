package registry

import (
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the Registry built from the fx command group. Core module.
func Module() fx.Option {
	return fx.Module("registry",
		logger.Decorate("registry"),
		fx.Provide(newCommandCounter),
		fx.Provide(newCommandDuration),
		fx.Provide(New),
	)
}
